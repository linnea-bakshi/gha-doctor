// Downloads the checksum-verified gha-doctor release binary for this platform.
// Zero dependencies. Runs at postinstall; the bin shim also calls it lazily
// when installs skip scripts (npm --ignore-scripts, some CI setups).
'use strict';

const crypto = require('crypto');
const fs = require('fs');
const https = require('https');
const path = require('path');
const zlib = require('zlib');

const pkg = require('./package.json');
const VERSION = pkg.version; // binary version is pinned to the npm version
const REPO = 'linnea-bakshi/gha-doctor';

function platformInfo() {
  // Test overrides let CI verify the extraction path for foreign platforms.
  const osName = process.env.GHA_DOCTOR_NPM_OS || process.platform;
  const archName = process.env.GHA_DOCTOR_NPM_ARCH || process.arch;
  const osMap = { linux: 'linux', darwin: 'darwin', win32: 'windows', windows: 'windows' };
  const archMap = { x64: 'amd64', amd64: 'amd64', arm64: 'arm64' };
  const goos = osMap[osName];
  const goarch = archMap[archName];
  if (!goos || !goarch) {
    throw new Error(
      `unsupported platform ${osName}/${archName}; supported: linux, macOS, windows on x64/arm64.\n` +
      'Other install options: https://github.com/linnea-bakshi/gha-doctor#install'
    );
  }
  const ext = goos === 'windows' ? 'zip' : 'tar.gz';
  return {
    goos,
    goarch,
    asset: `gha-doctor_${VERSION}_${goos}_${goarch}.${ext}`,
    binName: goos === 'windows' ? 'gha-doctor.exe' : 'gha-doctor',
  };
}

function fetch(url, redirects) {
  if (redirects === undefined) redirects = 5;
  return new Promise((resolve, reject) => {
    https
      .get(url, { headers: { 'user-agent': `gha-doctor-npm/${VERSION}` } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          if (redirects <= 0) return reject(new Error(`too many redirects for ${url}`));
          return resolve(fetch(new URL(res.headers.location, url).toString(), redirects - 1));
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(new Error(`GET ${url}: HTTP ${res.statusCode}`));
        }
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => resolve(Buffer.concat(chunks)));
        res.on('error', reject);
      })
      .on('error', reject);
  });
}

// Minimal tar reader: returns the entry whose basename matches `name`.
function extractFromTarGz(buf, name) {
  const tar = zlib.gunzipSync(buf);
  let off = 0;
  while (off + 512 <= tar.length) {
    const header = tar.subarray(off, off + 512);
    if (header.every((b) => b === 0)) break; // end-of-archive blocks
    const rawName = header.subarray(0, 100).toString('utf8').replace(/\0.*$/, '');
    const prefix = header.subarray(345, 500).toString('utf8').replace(/\0.*$/, '');
    const fullName = prefix ? `${prefix}/${rawName}` : rawName;
    const size = parseInt(header.subarray(124, 136).toString('utf8').replace(/\0.*$/, '').trim(), 8) || 0;
    const typeflag = String.fromCharCode(header[156]);
    off += 512;
    if ((typeflag === '0' || typeflag === '\0') && path.posix.basename(fullName) === name) {
      return tar.subarray(off, off + size);
    }
    off += Math.ceil(size / 512) * 512;
  }
  throw new Error(`${name} not found in archive`);
}

// Minimal zip reader (central directory walk), enough for goreleaser zips.
function extractFromZip(buf, name) {
  // Find end-of-central-directory record (no zip comment in our archives,
  // but scan backwards a little to be safe).
  let eocd = -1;
  for (let i = buf.length - 22; i >= Math.max(0, buf.length - 22 - 1024); i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) { eocd = i; break; }
  }
  if (eocd < 0) throw new Error('zip: end of central directory not found');
  const count = buf.readUInt16LE(eocd + 10);
  let off = buf.readUInt32LE(eocd + 16);
  for (let i = 0; i < count; i++) {
    if (buf.readUInt32LE(off) !== 0x02014b50) throw new Error('zip: bad central directory entry');
    const method = buf.readUInt16LE(off + 10);
    const csize = buf.readUInt32LE(off + 20);
    const nameLen = buf.readUInt16LE(off + 28);
    const extraLen = buf.readUInt16LE(off + 30);
    const commentLen = buf.readUInt16LE(off + 32);
    const localOff = buf.readUInt32LE(off + 42);
    const entryName = buf.subarray(off + 46, off + 46 + nameLen).toString('utf8');
    off += 46 + nameLen + extraLen + commentLen;
    if (path.posix.basename(entryName) !== name) continue;
    if (buf.readUInt32LE(localOff) !== 0x04034b50) throw new Error('zip: bad local header');
    const lNameLen = buf.readUInt16LE(localOff + 26);
    const lExtraLen = buf.readUInt16LE(localOff + 28);
    const dataStart = localOff + 30 + lNameLen + lExtraLen;
    const data = buf.subarray(dataStart, dataStart + csize);
    if (method === 0) return Buffer.from(data);
    if (method === 8) return zlib.inflateRawSync(data);
    throw new Error(`zip: unsupported compression method ${method}`);
  }
  throw new Error(`${name} not found in archive`);
}

async function install() {
  const info = platformInfo();
  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`;

  const sums = (await fetch(`${base}/checksums.txt`)).toString('utf8');
  const line = sums.split('\n').find((l) => l.trim().endsWith(`  ${info.asset}`) || l.trim().endsWith(` ${info.asset}`));
  if (!line) throw new Error(`${info.asset} not present in checksums.txt for v${VERSION}`);
  const wantSha = line.trim().split(/\s+/)[0];

  const archive = await fetch(`${base}/${info.asset}`);
  const gotSha = crypto.createHash('sha256').update(archive).digest('hex');
  if (gotSha !== wantSha) {
    throw new Error(`checksum mismatch for ${info.asset}: want ${wantSha}, got ${gotSha}`);
  }

  const bin = info.asset.endsWith('.zip')
    ? extractFromZip(archive, info.binName)
    : extractFromTarGz(archive, info.binName);

  const destDir = path.join(__dirname, 'dist');
  fs.mkdirSync(destDir, { recursive: true });
  const dest = path.join(destDir, info.binName);
  // Write via temp + rename so a concurrent shim never sees a partial binary.
  const tmp = `${dest}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, bin, { mode: 0o755 });
  fs.renameSync(tmp, dest);
  return dest;
}

module.exports = { install, platformInfo };

if (require.main === module) {
  install()
    .then((dest) => {
      process.stdout.write(`gha-doctor ${VERSION} installed (checksum verified): ${dest}\n`);
    })
    .catch((err) => {
      process.stderr.write(`gha-doctor install failed: ${err.message}\n`);
      process.stderr.write('Other install options (brew, releases, go install, docker):\n');
      process.stderr.write('  https://github.com/linnea-bakshi/gha-doctor#install\n');
      process.exit(1);
    });
}
