package channel

import (
	"fmt"
	"time"

	"github.com/potato-hash/groundskeeper/internal/gkdb"
)

// NotifyApproval sends a notification for a pending approval through the
// gateway. High-risk approvals route to critical channels; medium to warning;
// low to info (usually dropped by policy).
func NotifyApproval(gw *Gateway, a *gkdb.ApprovalRow) error {
	if gw == nil || a == nil {
		return nil
	}
	severity := SeverityInfo
	switch a.Risk {
	case gkdb.RiskHigh:
		severity = SeverityCritical
	case gkdb.RiskMedium:
		severity = SeverityWarning
	}
	n := &Notification{
		ID:        a.ID,
		ThreadID:  a.ThreadID,
		Severity:  severity,
		Message:   fmt.Sprintf("Approval needed (%s): %s", a.Risk, a.Summary),
		Channels:  gw.Policy.TargetsFor(severity),
		CreatedAt: time.Now(),
	}
	return gw.Send(n)
}
