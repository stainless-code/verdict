#!/usr/bin/env python3
"""Build fake verdict release assets for hermetic setup-verdict tests.

Creates, for one version, the exact layout install.sh/install.ps1 fetch:
<out>/v<version>/{archive, checksums.txt, checksums.txt.sigstore.json}.
The fake binary prints "verdict <version>" for any argv (so `--version`
smoke-tests pass). With --corrupt, checksums.txt carries a zeroed hash so
verification must fail. Also writes fakebin/cosign (and curl on POSIX) shims
that log their argv and exit a canned status, so no network or real Sigstore
is ever touched.

Layout mirrors evals/cases/installer-binary.sh (B1/B3 grades).
"""

import argparse
import hashlib
import os
import platform
import stat
import sys
import tarfile
import zipfile

BUNDLE = "{}\n"


def host_platform():
    system = platform.system()
    machine = platform.machine().lower()
    arch = "arm64" if machine in ("arm64", "aarch64") else "amd64"
    if system == "Darwin":
        return ("darwin", arch)
    if system == "Linux":
        return ("linux", arch)
    if system == "Windows":
        return ("windows", arch)
    raise SystemExit("unsupported host: %s/%s" % (system, machine))


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--version", required=True)
    ap.add_argument("--exe", default=None,
                    help="prebuilt fake verdict.exe (Windows; compiled by the caller)")
    ap.add_argument("--corrupt", action="store_true")
    ap.add_argument("--no-shims", action="store_true")
    args = ap.parse_args()

    goos, goarch = host_platform()
    version = args.version
    rel = os.path.join(args.out, "v" + version)
    os.makedirs(rel, exist_ok=True)
    fakebin = os.path.join(args.out, "fakebin")
    if not args.no_shims:
        os.makedirs(fakebin, exist_ok=True)

    if goos == "windows":
        if not args.exe:
            raise SystemExit("--exe is required on Windows")
        archive_name = "verdict_%s_windows_%s.zip" % (version, goarch)
        archive = os.path.join(rel, archive_name)
        with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as zf:
            zf.write(args.exe, "verdict.exe")
            zf.writestr("LICENSE", "")
            zf.writestr("README.md", "")
    else:
        script = "#!/bin/sh\necho \"verdict %s\"\n" % version
        staged = os.path.join(rel, "stage")
        os.makedirs(staged, exist_ok=True)
        for name, data, mode in (("verdict", script, 0o755),
                                 ("LICENSE", "", 0o644),
                                 ("README.md", "", 0o644)):
            p = os.path.join(staged, name)
            with open(p, "w", newline="\n") as f:
                f.write(data)
            os.chmod(p, mode)
        archive_name = "verdict_%s_%s_%s.tar.gz" % (version, goos, goarch)
        archive = os.path.join(rel, archive_name)
        with tarfile.open(archive, "w:gz", format=tarfile.PAX_FORMAT) as tf:
            for name in ("verdict", "LICENSE", "README.md"):
                p = os.path.join(staged, name)
                ti = tf.gettarinfo(p, arcname=name)
                ti.uid = ti.gid = 0
                ti.uname = ti.gname = ""
                ti.mtime = 0
                with open(p, "rb") as f:
                    tf.addfile(ti, f)

    digest = "0" * 64 if args.corrupt else sha256(archive)
    with open(os.path.join(rel, "checksums.txt"), "w", newline="\n") as f:
        f.write("%s  %s\n" % (digest, archive_name))
    with open(os.path.join(rel, "checksums.txt.sigstore.json"), "w", newline="\n") as f:
        f.write(BUNDLE)

    if not args.no_shims:
        if goos == "windows":
            with open(os.path.join(fakebin, "cosign.cmd"), "w", newline="\r\n") as f:
                f.write("@echo off\r\n"
                        "echo %*>>\"%FAKE_COSIGN_LOG%\"\r\n"
                        "if \"%FAKE_COSIGN_STATUS%\"==\"\" exit /b 0\r\n"
                        "exit /b %FAKE_COSIGN_STATUS%\r\n")
        else:
            for name, body in (
                ("cosign",
                 "#!/bin/sh\n"
                 'for a in "$@"; do printf \'%s \' "$a" >> "${FAKE_COSIGN_LOG:-/dev/null}"; done\n'
                 "printf '\\n' >> \"${FAKE_COSIGN_LOG:-/dev/null}\"\n"
                 'exit "${FAKE_COSIGN_STATUS:-0}"\n'),
                ("curl",
                 "#!/bin/sh\n"
                 "printf '%s\\n' \"$*\" >> \"${FAKE_CURL_LOG:-/dev/null}\"\n"
                 'for a in "$@"; do\n'
                 "  if [ \"$a\" = '%{url_effective}' ]; then printf '%s\\n' \"${FAKE_EFFECTIVE_URL:-}\"; exit 0; fi\n"
                 "done\n"
                 # Locate the real curl through the POSIX default PATH instead
                 # of `exec command -p curl`: dash treats the first operand of
                 # exec as a program, so that spelling dies with
                 # "exec: command: not found" on Ubuntu's /bin/sh.
                 'real="$(PATH="$(getconf PATH)"; command -v curl)" || exit 127\n'
                 'exec "$real" "$@"\n'),
            ):
                p = os.path.join(fakebin, name)
                with open(p, "w", newline="\n") as f:
                    f.write(body)
                os.chmod(p, os.stat(p).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

    print("fake release %s for %s/%s in %s" % (version, goos, goarch, rel))


if __name__ == "__main__":
    main()
