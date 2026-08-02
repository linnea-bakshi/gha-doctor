#!/usr/bin/env node
// Thin shim: runs the real gha-doctor binary, downloading it first if the
// install was done with --ignore-scripts.
'use strict';

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const installer = require('../install.js');

function binPath() {
  const info = installer.platformInfo();
  return path.join(__dirname, '..', 'dist', info.binName);
}

async function main() {
  let bin = binPath();
  if (!fs.existsSync(bin)) {
    process.stderr.write('gha-doctor binary not present (scripts skipped at install?); downloading…\n');
    bin = await installer.install();
  }
  const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
  if (res.error) throw res.error;
  if (res.signal) {
    process.kill(process.pid, res.signal);
    return;
  }
  process.exit(res.status === null ? 1 : res.status);
}

main().catch((err) => {
  process.stderr.write(`gha-doctor: ${err.message}\n`);
  process.exit(1);
});
