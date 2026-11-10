package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/core"
)

// Uninstall removes an installer-managed binary plus its receipt, and (on
// request) the marked PATH block. Every other channel is refused with the
// owning removal command; a binary without a receipt is never removed.
func Uninstall(ctx context.Context, req *Request) (Response, *core.RefusalError) {
	det, ref := detect(req)
	if ref != nil {
		return Response{}, ref
	}
	if det.Channel != ChannelInstaller {
		return Response{}, uninstallRefusal(det)
	}
	removed := []string{det.exe, det.Receipt}
	if req.DryRun {
		return Response{
			Channel: det.Channel, Action: ActionUninstall,
			Target: det.exe, Removed: removed, DryRun: true,
		}, nil
	}
	resp, ref := removeInstaller(det, removed)
	if ref != nil {
		return resp, ref
	}
	if req.RemovePath {
		if ref := removePathEntry(ctx, req, det, &resp); ref != nil {
			return resp, ref
		}
	}
	return resp, nil
}

func removePathEntry(ctx context.Context, req *Request, det *detection, resp *Response) *core.RefusalError {
	where, changed, err := removePathMarker(ctx, req.envMap(), det.InstallDir)
	if err != nil {
		return &core.RefusalError{
			Code:    codeUpgradeFailed,
			Message: "removed the binary but could not remove the PATH entry: " + err.Error(),
		}
	}
	resp.PathLineRemoved = changed
	if changed {
		req.human("uninstall: removed the marked PATH line from " + where)
	}
	return nil
}

func removeInstaller(det *detection, removed []string) (Response, *core.RefusalError) {
	if err := os.Remove(det.exe); err != nil {
		return Response{}, &core.RefusalError{Code: codeUpgradeFailed, Message: "cannot remove " + det.exe + ": " + err.Error()}
	}
	if err := os.Remove(det.Receipt); err != nil {
		return Response{}, &core.RefusalError{
			Code:    codeUpgradeFailed,
			Message: "removed the binary but not the receipt: " + err.Error(),
		}
	}
	_ = os.Remove(det.InstallDir) // succeeds only when empty; foreign files keep the dir
	return Response{
		Channel: det.Channel, Action: ActionUninstall,
		Target: det.exe, Removed: removed,
	}, nil
}

func uninstallRefusal(det *detection) *core.RefusalError {
	if within(det.exe, det.InstallDir) && !validReceipt(det.Receipt) {
		return &core.RefusalError{
			Code: codeNoReceipt,
			Message: fmt.Sprintf(
				"refusing to remove %s: no installer receipt (not installed by install.sh)", det.exe),
		}
	}
	return &core.RefusalError{
		Code: codeUninstallGate,
		Message: fmt.Sprintf("%s is not installer-managed; remove it with: %s",
			det.exe, uninstallCommand(det)),
	}
}

func uninstallCommand(det *detection) string {
	switch det.Channel {
	case ChannelNPM:
		return "npm uninstall -g " + npmPackage
	case ChannelPNPM:
		return "pnpm remove -g " + npmPackage
	case ChannelBun:
		return "bun remove -g " + npmPackage
	case ChannelYarn:
		return "yarn global remove " + npmPackage
	case ChannelBrew:
		return "brew uninstall " + brewTarget
	case ChannelProject:
		return fmt.Sprintf("cd %s && npm uninstall %s", projectRoot(filepath.Dir(det.exe)), npmPackage)
	case ChannelSkill:
		return "verdict skill uninstall --dir " + filepath.Dir(skillRoot(det.exe)) + " --force"
	default:
		return installerCommand()
	}
}
