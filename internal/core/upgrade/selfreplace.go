package upgrade

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/release"
)

// selfReplace performs the installer-managed upgrade: resolve the version,
// download and verify, smoke-test the staged binary, then atomically
// replace the target with the old binary kept aside until the receipt is
// written. Any failure restores or leaves the old binary in place.
func selfReplace(ctx context.Context, req *Request, det *detection) (Response, *core.RefusalError) {
	base := strings.TrimRight(req.env("VERDICT_DOWNLOAD_URL"), "/")
	version, ref := resolveVersion(ctx, req, base)
	if ref != nil {
		return Response{}, ref
	}
	stage, err := os.MkdirTemp(det.InstallDir, ".verdict-stage-")
	if err != nil {
		return Response{}, &core.RefusalError{
			Code: codeUpgradeFailed,
			Message: fmt.Sprintf("cannot stage an upgrade in %s: %s (set VERDICT_INSTALL; no root needed)",
				det.InstallDir, err),
		}
	}
	defer func() { _ = os.RemoveAll(stage) }() // staging is best-effort after the outcome is decided
	result, ref := release.FetchVerify(ctx, &release.Request{
		Client:           req.Client,
		RunCosign:        req.RunCosign,
		Version:          version,
		GOOS:             req.platform(),
		GOARCH:           runtime.GOARCH,
		BaseURL:          base,
		DownloadDir:      stage,
		StageDir:         stage,
		Cosign:           req.env("VERDICT_COSIGN"),
		RequireSignature: req.env("VERDICT_REQUIRE_SIGNATURE") == "1",
		Warn:             req.warn,
	})
	if ref != nil {
		return Response{}, ref
	}
	smoke := req.Smoke
	if smoke == nil {
		smoke = defaultSmoke
	}
	if err := smoke(ctx, result.Binary, version); err != nil {
		return Response{}, &core.RefusalError{
			Code: codeUpgradeFailed,
			Message: fmt.Sprintf("the downloaded verdict %s failed its --version smoke test (%s); "+
				"the installed binary is untouched", version, err),
		}
	}
	if req.platform() == "windows" {
		return windowsHandoff(ctx, req, det, &result, version)
	}
	if err := replaceInstalled(req, det, &result, version); err != nil {
		return Response{}, &core.RefusalError{Code: codeUpgradeFailed, Message: err.Error()}
	}
	return Response{
		Channel: ChannelInstaller,
		Action:  ActionSelfReplace,
		Version: version,
		Target:  det.exe,
		SHA256:  result.SHA256,
	}, nil
}

// resolveVersion applies the pin or resolves latest through the redirect.
func resolveVersion(ctx context.Context, req *Request, base string) (string, *core.RefusalError) {
	pin := strings.TrimPrefix(req.PinVersion, "v")
	if pin != "" {
		if !release.ValidVersion(pin) {
			return "", &core.RefusalError{Code: codeBadVersion, Message: "bad --version: " + req.PinVersion + " (use X.Y.Z or vX.Y.Z)"}
		}
		return pin, nil
	}
	version, err := release.LatestVersion(ctx, req.Client, base)
	if err != nil {
		return "", &core.RefusalError{Code: codeUpgradeFailed, Message: "could not resolve the latest release: " + err.Error()}
	}
	return version, nil
}

// replaceInstalled swaps the staged binary in, keeping a copy of the old
// binary until the receipt lands so any failure rolls back.
func replaceInstalled(req *Request, det *detection, result *release.Result, version string) error {
	backup := filepath.Join(filepath.Dir(result.Binary), "previous-"+filepath.Base(det.exe))
	if err := copyFile(det.exe, backup); err != nil {
		return newErrf("cannot back up %s: %s; the installed binary is untouched", det.exe, err.Error())
	}
	defer func() { _ = os.Remove(backup) }() // rollback already decided the outcome
	if err := atomic.Replace(result.Binary, det.exe); err != nil {
		return newErrf("atomic replace failed: %s; the old binary is untouched", err.Error())
	}
	body := receiptBody(filepath.Base(det.exe), version, receiptInstalledBy(det.Receipt, req.platform()))
	if err := atomic.WriteFile(det.Receipt, []byte(body)); err != nil {
		if rollback := atomic.Replace(backup, det.exe); rollback != nil {
			return newErrf("receipt write failed (%s) and rollback failed (%s)", err.Error(), rollback.Error())
		}
		return newErrf("receipt write failed: %s (old binary restored)", err.Error())
	}
	return nil
}

// windowsHandoff copies the verified new binary to %TEMP% and spawns it as
// the replacement helper: the running exe is never overwritten.
func windowsHandoff(
	ctx context.Context, req *Request, det *detection, result *release.Result, version string,
) (Response, *core.RefusalError) {
	dir, err := os.MkdirTemp("", "verdict-upgrade-")
	if err != nil {
		return Response{}, &core.RefusalError{Code: codeUpgradeFailed, Message: "cannot stage the Windows handoff: " + err.Error()}
	}
	helper := filepath.Join(dir, filepath.Base(det.exe))
	if err := copyFile(result.Binary, helper); err != nil {
		return Response{}, &core.RefusalError{Code: codeUpgradeFailed, Message: "cannot copy the new binary aside: " + err.Error()}
	}
	handoff := Handoff{
		ParentPID:   os.Getpid(),
		Target:      det.exe,
		New:         helper,
		ReceiptPath: det.Receipt,
		ReceiptBody: receiptBody(filepath.Base(det.exe), version, receiptInstalledBy(det.Receipt, req.platform())),
		Version:     version,
		ResultPath:  filepath.Join(dir, "result.json"),
	}
	logPath, err := spawnHandoff(ctx, req, helper, &handoff)
	if err != nil {
		return Response{}, &core.RefusalError{Code: codeUpgradeFailed, Message: "cannot start the upgrade helper: " + err.Error()}
	}
	return Response{
		Channel:      ChannelInstaller,
		Action:       ActionSelfReplace,
		Version:      version,
		Target:       det.exe,
		SHA256:       result.SHA256,
		Handoff:      true,
		HelperResult: handoff.ResultPath,
		HelperLog:    logPath,
	}, nil
}

// defaultSmoke runs `--version` on the staged binary and compares bytes.
func defaultSmoke(ctx context.Context, path, version string) error {
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.WaitDelay = commandWaitDelay
	out, err := cmd.Output()
	if err != nil {
		return jq.Sealed("run "+path+" --version", err)
	}
	if got := strings.TrimSpace(string(out)); got != version {
		return newErrf("--version printed %q; want %q", got, version)
	}
	return nil
}

// copyFile copies src to dst preserving the executable mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return jq.Sealed("open "+src, err)
	}
	defer func() { _ = in.Close() }() // read-only source; the copy error rules
	info, err := in.Stat()
	if err != nil {
		return jq.Sealed("stat "+src, err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return jq.Sealed("create "+dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // copy error rules
		return jq.Sealed("copy "+dst, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close() // sync error rules
		return jq.Sealed("sync "+dst, err)
	}
	if err := out.Close(); err != nil {
		return jq.Sealed("close "+dst, err)
	}
	return nil
}
