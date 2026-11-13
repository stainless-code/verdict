package core

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"
)

// ResolveProvider chooses an explicit provider or infers it from origin. An
// absent origin keeps the GitHub default for invocations outside a checkout.
func ResolveProvider(ctx context.Context, cmdr Commander, root, explicit string, env map[string]string) (string, string, *RefusalError) {
	if explicit != "" {
		return explicit, "flag", nil
	}
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	res, err := cmdr.Run(ctx, &Command{Dir: root, Argv: []string{"git", "remote", "get-url", "origin"}})
	if ctx.Err() != nil {
		return "", "", &RefusalError{Code: "canceled", Message: "provider lookup canceled or timed out"}
	}
	if err != nil || res.ExitCode != 0 {
		return providerGitHub, "default", nil
	}
	provider := providerFromRemote(strings.TrimSpace(res.Stdout), env)
	if provider == "" || res.StdoutTruncated {
		return "", "", &RefusalError{Code: codeProviderUnknown, Message: msgProviderUnknown}
	}
	return provider, "remote", nil
}

func scpRemote(raw string) string {
	at := strings.IndexByte(raw, '@')
	colon := strings.IndexByte(raw, ':')
	if at < 1 || colon <= at+1 || colon == len(raw)-1 {
		return ""
	}
	return "ssh://" + raw[:colon] + "/" + raw[colon+1:]
}

func remoteHost(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = scpRemote(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || !slices.Contains([]string{"https", "http", "ssh"}, u.Scheme) || u.Hostname() == "" || u.Path == "" ||
		u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func providerFromRemote(raw string, env map[string]string) string {
	host := remoteHost(raw)
	if host == "" {
		return ""
	}
	providers := map[string]string{
		"github.com": providerGitHub, "gitlab.com": providerGitLab, "gitcafe.com": providerGitCafe,
		"codeberg.org": providerForgejo, "bitbucket.org": providerBitbucket,
		"dev.azure.com": providerAzure, "ssh.dev.azure.com": providerAzure,
	}
	chosen := providers[host]
	for key, provider := range map[string]string{"GITLAB_HOST": providerGitLab, "FORGEJO_HOST": providerForgejo} {
		if configuredHost(env[key]) != host {
			continue
		}
		if chosen != "" && chosen != provider {
			return ""
		}
		chosen = provider
	}
	return chosen
}

func configuredHost(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

const (
	providerTimeout     = 10 * time.Second
	providerGitHub      = "github"
	providerGitLab      = "gitlab"
	providerGitCafe     = "gitcafe"
	providerForgejo     = "forgejo"
	providerBitbucket   = "bitbucket"
	providerAzure       = "azure"
	codeProviderUnknown = "provider-unknown"
)

const msgProviderUnknown = "cannot infer provider from origin — supported hosts: github.com, gitlab.com, " +
	"gitcafe.com, codeberg.org (Forgejo), bitbucket.org, dev.azure.com, ssh.dev.azure.com; " +
	"configured GITLAB_HOST and FORGEJO_HOST are also supported — " +
	"pass --provider github|gitlab|gitcafe|forgejo|bitbucket|azure"
