package main

// Groundskeeper durable-substrate CLI handlers (gk-* subcommands). These are
// additive to Agent Deck's existing command surface; the gk- prefix avoids
// collision until the TUI-integration phase decides whether to unify.

import (
	"bufio"
	"context"
	"os/exec"
	"flag"
	"fmt"
	"net/smtp"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/potato-hash/groundskeeper/internal/agentpaths"
	"github.com/potato-hash/groundskeeper/internal/channel"
	"github.com/potato-hash/groundskeeper/internal/fleet"
	"github.com/potato-hash/groundskeeper/internal/gkdb"
	"github.com/potato-hash/groundskeeper/internal/host"
	"github.com/potato-hash/groundskeeper/internal/runtime"
	"github.com/potato-hash/groundskeeper/internal/sidecar"
	"github.com/potato-hash/groundskeeper/internal/worker"
)

// gkDBPath resolves the Groundskeeper durable DB location:
// $XDG_DATA_HOME/groundskeeper/gk.db (fallback ~/.local/share/groundskeeper/gk.db).
func gkDBPath() (string, error) {
	dir, err := agentpaths.DataDir()
	if err != nil {
		return "", fmt.Errorf("gk: resolve data dir: %w", err)
	}
	return filepath.Join(dir, "gk.db"), nil
}

// openGk opens the durable DB and exits on error.
func openGk() *gkdb.DB {
	path, err := gkDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gk: %v\n", err)
		os.Exit(1)
	}
	db, err := gkdb.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gk: open %s: %v\n", path, err)
		os.Exit(1)
	}
	return db
}

// handleGkStatus prints counts: threads, running jobs, pending approvals, dead letters.
func handleGkStatus(args []string) {
	db := openGk()
	defer db.Close()

	threads, err := db.ListThreads(false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gk-status: list threads: %v\n", err)
		os.Exit(1)
	}
	running, err := db.ListJobs(gkdb.JobRunning)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gk-status: list jobs: %v\n", err)
		os.Exit(1)
	}
	pending, err := db.ListPendingApprovals()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gk-status: list approvals: %v\n", err)
		os.Exit(1)
	}
	dead, err := db.ListJobs(gkdb.JobDeadLetter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gk-status: list dead: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("threads: %d\n", len(threads))
	fmt.Printf("running jobs: %d\n", len(running))
	fmt.Printf("pending approvals: %d\n", len(pending))
	fmt.Printf("dead letters: %d\n", len(dead))
}

// handleGkThread dispatches thread subcommands: create, list, show, archive.
func handleGkThread(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: groundskeeper gk-thread <create|list|show|archive> ...")
		os.Exit(1)
	}
	db := openGk()
	defer db.Close()

	switch args[0] {
	case "create", "new":
		fs := flag.NewFlagSet("gk-thread create", flag.ExitOnError)
		title := fs.String("title", "", "thread title")
		runtime := fs.String("runtime", "omp", "agent runtime (omp)")
		workspace := fs.String("workspace", ".", "workspace path")
		fs.Parse(args[1:])
		if *title == "" {
			fmt.Fprintln(os.Stderr, "gk-thread create: --title is required")
			os.Exit(1)
		}
		abs, err := filepath.Abs(*workspace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread create: workspace: %v\n", err)
			os.Exit(1)
		}
		t, err := db.CreateThread(*title, *runtime, abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread create: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(t.ID)
	case "list", "ls":
		includeArchived := false
		for _, a := range args[1:] {
			if a == "--archived" || a == "-a" {
				includeArchived = true
			}
		}
		threads, err := db.ListThreads(includeArchived)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread list: %v\n", err)
			os.Exit(1)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tTITLE\tRUNTIME\tSTATUS\tWORKSPACE")
		for _, t := range threads {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Title, t.Runtime, t.Status, t.WorkspacePath)
		}
		w.Flush()
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: gk-thread show <id>")
			os.Exit(1)
		}
		t, err := db.GetThread(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread show: %v\n", err)
			os.Exit(1)
		}
		if t == nil {
			fmt.Fprintf(os.Stderr, "gk-thread show: not found: %s\n", args[1])
			os.Exit(1)
		}
		fmt.Printf("id: %s\n", t.ID)
		fmt.Printf("title: %s\n", t.Title)
		fmt.Printf("runtime: %s\n", t.Runtime)
		fmt.Printf("status: %s\n", t.Status)
		fmt.Printf("workspace: %s\n", t.WorkspacePath)
		fmt.Printf("session_dir: %s\n", t.SessionDir)
	case "archive":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: gk-thread archive <id>")
			os.Exit(1)
		}
		if err := db.ArchiveThread(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread archive: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("archived: %s\n", args[1])
	case "prompt":
		// Enqueue a turn job for the thread with the given prompt as the goal.
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: gk-thread prompt <id> <prompt text>")
			os.Exit(1)
		}
		prompt := strings.Join(args[2:], " ")
		if err := db.SetThreadGoal(args[1], prompt); err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread prompt: %v\n", err)
			os.Exit(1)
		}
		j, err := db.CreateJob(args[1], "turn")
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread prompt: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("job enqueued: %s\n", j.ID)
	case "resume":
		// Mark the thread resumable (the daemon will resume its session_dir).
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: gk-thread resume <id>")
			os.Exit(1)
		}
		t, err := db.GetThread(args[1])
		if err != nil || t == nil {
			fmt.Fprintf(os.Stderr, "gk-thread resume: not found: %s\n", args[1])
			os.Exit(1)
		}
		j, _ := db.CreateJob(args[1], "turn")
		fmt.Printf("resumed: %s (job: %s)\n", args[1], j.ID)
	case "fork":
		// Create a child thread preserving parent metadata.
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: gk-thread fork <parent-id> [--title ...]")
			os.Exit(1)
		}
		fs := flag.NewFlagSet("gk-thread fork", flag.ExitOnError)
		title := fs.String("title", "", "child thread title")
		fs.Parse(args[2:])
		parent, err := db.GetThread(args[1])
		if err != nil || parent == nil {
			fmt.Fprintf(os.Stderr, "gk-thread fork: parent not found: %s\n", args[1])
			os.Exit(1)
		}
		t, err := db.ForkThread(parent, *title)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-thread fork: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(t.ID)
	}
}

// handleGkApprovals dispatches approval subcommands: list (default), approve, reject.
func handleGkApprovals(args []string) {
	if len(args) == 0 {
		args = []string{"list"}
	}
	db := openGk()
	defer db.Close()

	switch args[0] {
	case "list", "ls":
		pending, err := db.ListPendingApprovals()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-approvals list: %v\n", err)
			os.Exit(1)
		}
		if len(pending) == 0 {
			fmt.Println("no pending approvals")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tRISK\tSUMMARY\tJOB")
		for _, a := range pending {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, a.Risk, a.Summary, a.JobID)
		}
		w.Flush()
	case "approve":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: gk-approvals approve <id>")
			os.Exit(1)
		}
		if err := db.ResolveApproval(args[1], true, "cli"); err != nil {
			fmt.Fprintf(os.Stderr, "gk-approvals approve: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("approved: %s\n", args[1])
	case "reject":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: gk-approvals reject <id>")
			os.Exit(1)
		}
		if err := db.ResolveApproval(args[1], false, "cli"); err != nil {
			fmt.Fprintf(os.Stderr, "gk-approvals reject: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("rejected: %s\n", args[1])
	default:
		fmt.Fprintf(os.Stderr, "gk-approvals: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
}

// handleGkDaemon runs the Groundskeeper worker daemon: claims jobs from the
// durable DB and dispatches them to OMP workers via the runtime adapter.
// Blocks until SIGINT/SIGTERM. --model selects the omp model; --slots bounds
// concurrency.
func handleGkDaemon(args []string) {
	fs := flag.NewFlagSet("gk-daemon", flag.ExitOnError)
	model := fs.String("model", "", "omp model (e.g. ollama-cloud/glm-5.2)")
	slots := fs.Int("slots", 4, "max concurrent workers")
	fake := fs.Bool("fake", false, "use the fake adapter (no real omp)")
	sidecarURL := fs.String("sidecar", "", "sidecar URL for notifications (HMAC-signed)")
	hmacKey := fs.String("hmac-key", "", "HMAC signing key shared with the sidecar (env GK_HMAC_KEY if empty)")
	espalierPath := fs.String("espalier-path", "", "path to Espalier Core extension (loads it into OMP workers)")
	sshTarget := fs.String("ssh", "", "remote SSH target (user@host) to spawn omp on a remote machine")
	sshOmpBin := fs.String("ssh-omp-bin", "omp", "path to omp on the remote host")
	fs.Parse(args)
	if *hmacKey == "" {
		*hmacKey = os.Getenv("GK_HMAC_KEY")
	}

	db := openGk()
	defer db.Close()

	var adapter runtime.AgentRuntimeAdapter
	bridge := host.NewBridge(db)
	if *fake {
		adapter = runtime.NewFakeAdapter()
	} else {
		adapter = runtime.NewOmpAdapter(runtime.OmpAdapterConfig{
			Model:       *model,
			HostHandler: bridge,
			HostTools:   hostToolDefinitions(bridge),
			ExtraArgs:   esplalierArgs(*espalierPath),
			SSHTarget:   *sshTarget,
			SSHOmpBin:   *sshOmpBin,
		})
	}

	pool := worker.New(db, adapter, worker.Config{MaxSlots: *slots})
	pool.SetLogger(nil) // use default slog

	// Wire the notification gateway if a sidecar URL is given.
	if *sidecarURL != "" {
		gw := channel.NewGateway(channel.DefaultPolicy(),
			&channel.SidecarClient{BaseURL: *sidecarURL, Key: []byte(*hmacKey)})
		pool.SetGateway(gw)
		fmt.Printf("gk-daemon: notifications via sidecar %s\n", *sidecarURL)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ngk-daemon: shutting down...")
		cancel()
	}()

	sshInfo := ""
	if *sshTarget != "" {
		sshInfo = " ssh=" + *sshTarget
	}
	fmt.Printf("gk-daemon: running (%d slots, model=%q, adapter=%s%s)\n",
		*slots, *model, adapterType(adapter), sshInfo)
	pool.Start(ctx)
	<-ctx.Done()
	pool.Stop()
	fmt.Println("gk-daemon: stopped")
}

func adapterType(a runtime.AgentRuntimeAdapter) string {
	switch a.(type) {
	case *runtime.FakeAdapter:
		return "fake"
	case *runtime.OmpAdapter:
		return "omp"
	default:
		return "unknown"
	}
}

// handleGkJob dispatches job subcommands: create (enqueue a turn job for a
// thread), list, show.
func handleGkJob(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: groundskeeper gk-job <create|list|show> ...")
		os.Exit(1)
	}
	db := openGk()
	defer db.Close()
	switch args[0] {
	case "create", "new":
		fs := flag.NewFlagSet("gk-job create", flag.ExitOnError)
		threadID := fs.String("thread", "", "thread id to enqueue a turn for")
		kind := fs.String("kind", "turn", "job kind")
		fs.Parse(args[1:])
		if *threadID == "" {
			fmt.Fprintln(os.Stderr, "gk-job create: --thread is required")
			os.Exit(1)
		}
		j, err := db.CreateJob(*threadID, *kind)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-job create: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(j.ID)
	case "list", "ls":
		status := ""
		if len(args) > 1 {
			status = args[1]
		}
		jobs, err := db.ListJobs(status)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-job list: %v\n", err)
			os.Exit(1)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tTHREAD\tSTATUS\tATTEMPTS\tKIND")
		for _, j := range jobs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", j.ID, j.ThreadID, j.Status, j.Attempts, j.Kind)
		}
		w.Flush()
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: gk-job show <id>")
			os.Exit(1)
		}
		j, err := db.GetJob(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "gk-job show: %v\n", err)
			os.Exit(1)
		}
		if j == nil {
			fmt.Fprintf(os.Stderr, "gk-job show: not found: %s\n", args[1])
			os.Exit(1)
		}
		fmt.Printf("id: %s\n", j.ID)
		fmt.Printf("thread: %s\n", j.ThreadID)
		fmt.Printf("status: %s\n", j.Status)
		fmt.Printf("kind: %s\n", j.Kind)
		fmt.Printf("attempts: %d/%d\n", j.Attempts, j.MaxAttempts)
	default:
		fmt.Fprintf(os.Stderr, "gk-job: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
}

// handleFleet prints the Groundskeeper fleet status (threads, running jobs,
// pending approvals, dead letters) as a single read. This is the unified
// command surface for the TUI/CLI fleet view.
func handleFleet(args []string) {
	db := openGk()
	defer db.Close()
	v, err := fleet.Load(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fleet: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(v.RenderText())
}

// handleGkSidecar launches a credential-holding sidecar process. The sidecar
// verifies HMAC-signed delivery requests from the daemon and performs the
// privileged action (email/calendar/contact) with a credential it holds — the
// daemon never sees it. --kind selects the handler; --addr the listen address;
// --hmac-key the shared signing key (env GK_HMAC_KEY if empty).
func handleGkSidecar(args []string) {
	fs := flag.NewFlagSet("gk-sidecar", flag.ExitOnError)
	kind := fs.String("kind", "email", "sidecar kind: email|calendar|contact")
	addr := fs.String("addr", "127.0.0.1:7780", "listen address")
	hmacKey := fs.String("hmac-key", "", "HMAC shared key (env GK_HMAC_KEY if empty)")
	fs.Parse(args)
	if *hmacKey == "" {
		*hmacKey = os.Getenv("GK_HMAC_KEY")
	}
	if *hmacKey == "" {
		fmt.Fprintln(os.Stderr, "gk-sidecar: --hmac-key or GK_HMAC_KEY is required")
		os.Exit(1)
	}

	var handler sidecar.Handler
	switch *kind {
	case "email":
		handler = &sidecar.EmailHandler{
			Addr:       os.Getenv("GK_SMTP_ADDR"),
			From:       os.Getenv("GK_SMTP_FROM"),
			Auth:       smtpAuthFromEnv(),
			Recipients: []string{os.Getenv("GK_NOTIFY_TO")},
		}
	case "calendar":
		handler = &sidecar.CalendarHandler{
			CalendarID: os.Getenv("GK_CAL_ID"),
			Token:      os.Getenv("GK_CAL_TOKEN"),
		}
	case "contact":
		handler = &sidecar.ContactHandler{
			Token: os.Getenv("GK_CONTACT_TOKEN"),
		}
	default:
		fmt.Fprintf(os.Stderr, "gk-sidecar: unknown kind %q\n", *kind)
		os.Exit(1)
	}

	srv := sidecar.NewServer(sidecar.Config{
		Addr:    *addr,
		HMACKey: []byte(*hmacKey),
		Handler: handler,
	})
	fmt.Printf("gk-sidecar: %s listening on %s\n", *kind, *addr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "gk-sidecar: %v\n", err)
		os.Exit(1)
	}
}

// smtpAuthFromEnv builds an smtp.Auth from env vars (best-effort).
func smtpAuthFromEnv() smtp.Auth {
	user := os.Getenv("GK_SMTP_USER")
	pass := os.Getenv("GK_SMTP_PASS")
	smtpHost := os.Getenv("GK_SMTP_HOST")
	if user == "" || pass == "" || smtpHost == "" {
		return nil
	}
	return smtp.PlainAuth("", user, pass, smtpHost)
}

// hostToolDefinitions builds the RpcHostToolDefinition list from the bridge's
// registered tools, so OMP knows which host tools Groundskeeper offers.
func hostToolDefinitions(b *host.Bridge) []runtime.RpcHostToolDefinition {
	names := b.ToolNames()
	out := make([]runtime.RpcHostToolDefinition, 0, len(names))
	for _, n := range names {
		out = append(out, runtime.RpcHostToolDefinition{
			Name: n, Description: "Groundskeeper host tool: " + n,
		})
	}
	return out
}


// handleLoop dispatches loop subcommands: set, start, stop, show.
func handleLoop(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: groundskeeper loop <set|start|stop|show> ...")
		os.Exit(1)
	}
	db := openGk()
	defer db.Close()
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("loop set", flag.ExitOnError)
		mode := fs.String("mode", "until_done", "loop mode: manual|until_done|interval|watcher|review_retry")
		prompt := fs.String("prompt", "", "loop prompt (or --prompt-file)")
		promptFile := fs.String("prompt-file", "", "read loop prompt from file")
		maxTurns := fs.Int("max-turns", 8, "max turns")
		maxWall := fs.Int("max-wall-minutes", 45, "max wall minutes")
		maxTools := fs.Int("max-tool-calls", 80, "max tool calls")
		maxCost := fs.Float64("max-cost", 0, "max cost USD (0=unlimited)")
		stopWhen := fs.String("stop-when", "agent_says_done", "stop condition")
		fs.Parse(args[2:])
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: loop set <thread-id> --mode ... --prompt ...")
			os.Exit(1)
		}
		threadID := args[1]
		if *promptFile != "" {
			b, err := os.ReadFile(*promptFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "loop set: read prompt-file: %v\n", err)
				os.Exit(1)
			}
			*prompt = string(b)
		}
		_, err := db.CreateLoopSpec(threadID, *mode, *prompt,
			int64(*maxTurns), int64(*maxWall), int64(*maxTools), *maxCost, *stopWhen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loop set: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("loop set: %s mode=%s\n", threadID, *mode)
	case "start":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: loop start <thread-id>")
			os.Exit(1)
		}
	if err := db.SetLoopEnabled(args[1], true); err != nil {
		fmt.Fprintf(os.Stderr, "loop start: %v\n", err)
		os.Exit(1)
	}
	// Create a loop_run and enqueue the first turn associated with it.
	spec, _ := db.GetLoopSpec(args[1])
	specID := ""
	if spec != nil {
		specID = spec.ID
	}
	run, err := db.StartLoopRun(args[1], specID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loop start: %v\n", err)
		os.Exit(1)
	}
	_, _ = db.IncrementTurnEnqueued(run.ID) // first turn
	j, err := db.CreateJobWithLoop(args[1], "turn", run.ID, "turn-1")
	if err != nil {
		fmt.Fprintf(os.Stderr, "loop start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("loop started: %s (run: %s, job: %s)\n", args[1], run.ID, j.ID)
	case "stop":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: loop stop <thread-id>")
			os.Exit(1)
		}
		if err := db.SetLoopEnabled(args[1], false); err != nil {
			fmt.Fprintf(os.Stderr, "loop stop: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("loop stopped: %s\n", args[1])
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: loop show <thread-id>")
			os.Exit(1)
		}
		spec, err := db.GetLoopSpec(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "loop show: %v\n", err)
			os.Exit(1)
		}
		if spec == nil {
			fmt.Println("no loop spec for this thread")
			return
		}
		fmt.Printf("mode: %s\n", spec.Mode)
		fmt.Printf("prompt: %s\n", spec.Prompt)
		fmt.Printf("max_turns: %d\n", spec.MaxTurns)
		fmt.Printf("max_wall_minutes: %d\n", spec.MaxWallMinutes)
		fmt.Printf("max_tool_calls: %d\n", spec.MaxToolCalls)
		fmt.Printf("stop_when: %s\n", spec.StopWhen)
		fmt.Printf("enabled: %v\n", spec.Enabled)
	default:
		fmt.Fprintf(os.Stderr, "loop: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
}

// handleEspalier reports Espalier Core readiness without importing its internals.
func handleEspalier(args []string) {
	if len(args) == 0 || args[0] == "status" {
		fmt.Println("Espalier Core readiness check:")
		// Check for the espalier extension dir / package.
		espalierPath := os.Getenv("GK_ESPALIER_PATH")
		if espalierPath == "" {
			espalierPath = filepath.Join(os.Getenv("HOME"), "espalier")
		}
		if _, err := os.Stat(espalierPath); err == nil {
			fmt.Printf("  package path: %s (found)\n", espalierPath)
		} else {
			fmt.Printf("  package path: %s (missing — degraded)\n", espalierPath)
		}
		// Check for a watchdog file.
		watchdog := os.Getenv("GK_ESPALIER_WATCHDOG")
		if watchdog != "" {
			if _, err := os.Stat(watchdog); err == nil {
				fmt.Printf("  watchdog: %s (found)\n", watchdog)
			} else {
				fmt.Printf("  watchdog: %s (missing)\n", watchdog)
			}
		} else {
			fmt.Println("  watchdog: not configured")
		}
		// OMP can see the extension if the extension entry exists.
		fmt.Println("  Groundskeeper does not import Espalier learning internals.")
		fmt.Println("  Worker launch with Espalier configured: --espalier-path flag on gk-daemon")
		return
	}
	fmt.Fprintf(os.Stderr, "espalier: unknown subcommand %q\n", args[0])
	os.Exit(1)
}

// handleAuthStatus reports OMP/provider auth status without storing tokens.
func handleAuthStatus(args []string) {
	fmt.Println("Provider auth is managed by OMP.")
	fmt.Println()
	// Check omp on PATH.
	if _, err := exec.LookPath("omp"); err != nil {
		fmt.Println("Detected:")
		fmt.Println("  - omp on PATH: no (degraded)")
		fmt.Println()
		fmt.Println("Install OMP to configure providers.")
		return
	}
	fmt.Println("Detected:")
	fmt.Println("  - omp on PATH: yes")
	// Check for OMP auth-broker env (without printing the value).
	if os.Getenv("OMP_AUTH_BROKER_TOKEN") != "" {
		fmt.Println("  - OMP auth-broker env: configured")
	} else {
		fmt.Println("  - OMP auth-broker env: not set")
	}
	// Check for the agent.db (credentials exist, but we never read them).
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".omp", "agent", "agent.db")); err == nil {
		fmt.Println("  - OMP credential store: present")
	} else {
		fmt.Println("  - OMP credential store: not found")
	}
	fmt.Println()
	fmt.Println("To configure providers:")
	fmt.Println("  open OMP and run /login <provider>")
	fmt.Println("  or use OMP auth-broker login for headless/shared setups.")
	fmt.Println()
	fmt.Println("Groundskeeper does not store provider tokens.")
}

// exec is imported via os/exec in main.go; use the package-level exec from there.
// We need a local exec.LookPath — use os/exec directly.

// esplalierArgs returns the omp --extension flags to load Espalier Core into
// a worker, or nil if no path is given. Groundskeeper passes the extension path;
// it never imports Espalier internals.
func esplalierArgs(path string) []string {
	if path == "" {
		return nil
	}
	return []string{"--extension", path}
}

// handleSetup is the full-stack installer and first-run onboarding. It installs
// or verifies the whole stack — Groundskeeper (this binary), OMP (the agent
// runtime), and Espalier Core (the learning extension) — prompts for model
// configuration, creates the gk.db, and prints a getting-started guide.
//
// The flow is interactive (prompts via stdin) unless --non-interactive is passed.
// Safe to re-run: each step is idempotent (checks before installing).
func handleSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	nonInteractive := fs.Bool("non-interactive", false, "skip all prompts (for CI)")
	fs.Parse(args)
	reader := bufio.NewReader(os.Stdin)
	prompt := func(question string) string {
		if *nonInteractive {
			return ""
		}
		fmt.Printf("%s ", question)
		line, _ := reader.ReadString('\n')
		return strings.TrimSpace(line)
	}
	confirm := func(question string) bool {
		if *nonInteractive {
			return false
		}
		return prompt(question+" [y/N]") == "y"
	}

	fmt.Println()
	fmt.Println("  ╔════════════════════════════════════════╗")
	fmt.Println("  ║     Groundskeeper Stack Installer      ║")
	fmt.Println("  ╚════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("  This installs the full Groundskeeper stack:")
	fmt.Println("    1. OMP — the agent runtime (omp --mode rpc)")
	fmt.Println("    2. Espalier Core — the learning/gating extension")
	fmt.Println("    3. Groundskeeper — the durable substrate + TUI")
	fmt.Println()

	// ── Step 1: OMP ──
	fmt.Println("── 1/5 · OMP runtime ──────────────────────")
	fmt.Println()
	ompPath, err := exec.LookPath("omp")
	if err == nil {
		fmt.Printf("  [OK] omp found at %s\n", ompPath)
		out, vErr := exec.Command(ompPath, "--version").Output()
		if vErr == nil {
			fmt.Printf("  [OK] version: %s", string(out))
		}
	} else {
		fmt.Println("  [MISSING] omp is not on PATH.")
		if confirm("  Install OMP now?") {
			if err := installOMP(); err != nil {
				fmt.Fprintf(os.Stderr, "  [ERROR] %v\n", err)
			} else {
				fmt.Println("  [OK] OMP installed to ~/.local/bin/omp")
				ompPath = filepath.Join(os.Getenv("HOME"), ".local", "bin", "omp")
			}
		} else {
			fmt.Println("  [SKIP] Install OMP manually: https://github.com/can1357/oh-my-pi")
		}
	}
	fmt.Println()

	// ── Step 2: Espalier Core ──
	fmt.Println("── 2/5 · Espalier Core ───────────────────")
	fmt.Println()
	espalierPath := os.Getenv("GK_ESPALIER_PATH")
	if espalierPath == "" {
		espalierPath = filepath.Join(os.Getenv("HOME"), "espalier")
	}
	if _, err := os.Stat(filepath.Join(espalierPath, "dist", "extensions", "index.js")); err == nil {
		fmt.Printf("  [OK] Espalier found at %s (dist built)\n", espalierPath)
	} else if _, err := os.Stat(espalierPath); err == nil {
		fmt.Printf("  [PARTIAL] %s exists but dist/ is not built\n", espalierPath)
		if confirm("  Build Espalier now? (requires bun)") {
			if err := buildEspalier(espalierPath); err != nil {
				fmt.Fprintf(os.Stderr, "  [ERROR] %v\n", err)
			} else {
				fmt.Println("  [OK] Espalier built")
			}
		}
	} else {
		fmt.Printf("  [MISSING] Espalier not found at %s\n", espalierPath)
		if confirm("  Clone and build Espalier now?") {
			if err := installEspalier(espalierPath); err != nil {
				fmt.Fprintf(os.Stderr, "  [ERROR] %v\n", err)
			} else {
				fmt.Println("  [OK] Espalier installed and built")
			}
		} else {
			fmt.Println("  [SKIP] Workers will run without Espalier (degraded)")
		}
	}
	fmt.Println()

	// ── Step 3: Groundskeeper durable DB ──
	fmt.Println("── 3/5 · Groundskeeper durable substrate ──")
	fmt.Println()
	dbPath, err := gkDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [ERROR] %v\n", err)
		os.Exit(1)
	}
	db, err := gkdb.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [ERROR] %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Printf("  [OK] gk.db at %s\n", dbPath)
	fmt.Println("  Tables: agent_threads, jobs, approvals, audit_events, loop_specs, loop_runs, dead_letters")
	fmt.Println()

	// ── Step 4: Model configuration ──
	fmt.Println("── 4/5 · Model configuration ─────────────")
	fmt.Println()
	model := os.Getenv("GK_OMP_MODEL")
	if model == "" {
		model = "ollama-cloud/glm-5.2"
	}
	if ompPath != "" {
		credPath := filepath.Join(os.Getenv("HOME"), ".omp", "agent", "agent.db")
		if _, err := os.Stat(credPath); err == nil {
			fmt.Println("  [OK] OMP credential store found")
		} else {
			fmt.Println("  [NOT FOUND] No OMP credential store")
			fmt.Println("  Run 'omp /login <provider>' to configure a provider.")
		}
	}
	inputModel := prompt(fmt.Sprintf("  Default model for workers (press Enter for %s):", model))
	if inputModel != "" {
		model = inputModel
	}
	fmt.Printf("  [SET] model = %s\n", model)
	fmt.Println("  Override per-run: gk-daemon --model <model>")
	fmt.Println()

	// ── Step 5: Tmux (dependency check, not install) ──
	fmt.Println("── 5/5 · Dependencies ────────────────────")
	fmt.Println()
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Println("  [MISSING] tmux (required for Agent Deck session management)")
		fmt.Println("  Install: brew install tmux  (macOS)  or  apt install tmux  (Linux)")
	} else {
		fmt.Println("  [OK] tmux found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Println("  [MISSING] git (required for worktree creation)")
	} else {
		fmt.Println("  [OK] git found")
	}
	if _, err := exec.LookPath("bun"); err != nil {
		fmt.Println("  [OPTIONAL] bun not found (needed to build Espalier)")
	} else {
		fmt.Println("  [OK] bun found")
	}
	fmt.Println()

	// ── Summary + getting started ──
	fmt.Println("══════════════════════════════════════════")
	fmt.Println()
	fmt.Println("  Setup complete!")
	fmt.Println()
	fmt.Println("  Quick start:")
	fmt.Println("    groundskeeper gk-thread create --title \"Fix tests\" --runtime omp --workspace .")
	fmt.Printf("    groundskeeper loop set <thread-id> --mode until_done --prompt \"Fix the test\" --max-turns 5\n")
	fmt.Printf("    groundskeeper loop start <thread-id>\n")
	fmt.Printf("    groundskeeper gk-daemon --model %s --slots 2\n", model)
	fmt.Println("    groundskeeper fleet")
	fmt.Println("    groundskeeper")
	fmt.Println()
	fmt.Println("  In the TUI, press tab to switch to Groundskeeper threads.")
	fmt.Println("  p = prompt, f = fork, a = archive")
	fmt.Println()
}

// installOMP downloads and installs the omp binary from GitHub releases.
func installOMP() error {
	// OMP is installed via its own installer script (can1357/oh-my-pi).
	// We delegate to OMP's install method rather than reimplementing it.
	fmt.Println("  Downloading OMP installer...")
	cmd := exec.Command("bash", "-c",
		"curl -fsSL https://raw.githubusercontent.com/can1357/oh-my-pi/main/install.sh | bash")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// installEspalier clones and builds the Espalier Core extension.
func installEspalier(path string) error {
	fmt.Printf("  Cloning Espalier to %s...\n", path)
	cmd := exec.Command("git", "clone", "https://github.com/potato-hash/espalier.git", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone espalier: %w", err)
	}
	return buildEspalier(path)
}

// buildEspalier runs bun install + bun build in the espalier directory.
func buildEspalier(path string) error {
	if _, err := exec.LookPath("bun"); err != nil {
		return fmt.Errorf("bun is required to build Espalier: install from https://bun.sh")
	}
	fmt.Println("  Installing Espalier dependencies (bun install)...")
	cmd := exec.Command("bun", "install")
	cmd.Dir = path
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bun install: %w", err)
	}
	fmt.Println("  Building Espalier (bun build)...")
	cmd = exec.Command("bun", "run", "build")
	cmd.Dir = path
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
