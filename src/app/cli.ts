#!/usr/bin/env node
// Groundskeeper CLI — minimal status command for now.
// Full daemon, worker manager, and channel gateway are future phases.

export async function main(args = process.argv.slice(2)): Promise<void> {
  const command = args[0] ?? "help";
  if (command === "help") return help();
  console.error(`Unknown command: ${command}`);
  help();
  process.exitCode = 2;
}

function help(): void {
  console.log(`Groundskeeper — autonomous agent shell for Espalier + OMP

Usage:
  groundskeeper help

Status: substrate extracted from Espalier Core. Daemon, worker manager,
and channels are future phases. See docs/roadmap.md.
`);
}