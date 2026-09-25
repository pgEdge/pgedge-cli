#!/usr/bin/env node
"use strict";

const { spawn } = require("node:child_process");
const path = require("node:path");

const pkg = `@pgedge/cli-${process.platform}-${process.arch}`;
const exe = process.platform === "win32" ? "pgedge.exe" : "pgedge";

let bin;
try {
    bin = path.join(path.dirname(require.resolve(`${pkg}/package.json`)), "bin", exe);
} catch {
    console.error(
        `pgedge: ${pkg} is not installed, so there is no binary for ` +
            `${process.platform}-${process.arch}. Reinstall without ` +
            "--omit=optional or --no-optional, or install from " +
            "https://github.com/pgEdge/pgedge-cli/releases.",
    );
    process.exit(1);
}

const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });

// Registering a handler stops Node exiting on the signal, so the
// binary decides how to stop and its exit status reaches the caller.
const signals = ["SIGINT", "SIGTERM", "SIGHUP"];
for (const sig of signals) {
    process.on(sig, () => child.kill(sig));
}

child.on("error", (err) => {
    console.error(`pgedge: cannot run ${bin}: ${err.message}`);
    process.exit(1);
});

child.on("exit", (code, signal) => {
    if (signal) {
        for (const sig of signals) {
            process.removeAllListeners(sig);
        }
        process.kill(process.pid, signal);
        return;
    }
    process.exit(code ?? 1);
});
