package postcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestReplyThreadArgv pins the exact host calls drain makes and the body each
// one carries. The body is one argv field on GitHub and one stdin document on
// GitLab; no shell or interpolation sits between the draft and the host.
func TestReplyThreadArgv(t *testing.T) {
	const body = "answer with @alice and `quotes`\n"
	cases := []struct {
		name, provider, call, stdin, id string
	}{
		{
			name: "github", provider: "github", id: "77",
			call: "gh\x00api\x00--method\x00POST\x00repos/acme/app/pulls/7/comments/101/replies\x00-f\x00body=" + body,
		},
		{
			name: "gitlab", provider: "gitlab", id: "88",
			call:  "glab\x00api\x00projects/acme%2Fapp/merge_requests/7/discussions/PRRT_1/notes\x00--method\x00POST\x00--header\x00Content-Type: application/json\x00--input\x00-",
			stdin: "{\"body\":\"answer with @alice and `quotes`\\n\"}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
				tc.call: {stdout: `{"id":` + tc.id + `}`},
			}}
			got, ref := ReplyThread(context.Background(), cmdr, tc.provider, nil,
				"acme/app", "7", "101", "PRRT_1", body)
			if ref != nil {
				t.Fatalf("ReplyThread refused: %+v", ref)
			}
			if got != tc.id {
				t.Errorf("reply id = %q, want %q", got, tc.id)
			}
			if len(cmdr.calls) != 1 || strings.Join(cmdr.calls[0], "\x00") != tc.call {
				t.Fatalf("calls = %q, want %q", cmdr.calls, tc.call)
			}
			if tc.stdin != "" {
				doc, err := jq.Decode([]byte(cmdr.stdin[0]))
				obj, ok := jq.AsMap(doc)
				if err != nil || !ok || jq.R(jq.Alt(obj["body"], "")) != body {
					t.Errorf("stdin body = %q (err %v), want %q", cmdr.stdin[0], err, body)
				}
			}
		})
	}
}

// TestReplyThreadRefusesUnsupportedHost pins the capability gate: Forgejo gets
// a typed refusal and the host is never called.
func TestReplyThreadRefusesUnsupportedHost(t *testing.T) {
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	_, ref := ReplyThread(context.Background(), cmdr, "forgejo", nil,
		"acme/app", "7", "101", "PRRT_1", "answer")
	if ref == nil || ref.Code != "provider-unimplemented" {
		t.Fatalf("refusal = %+v, want provider-unimplemented", ref)
	}
	if !strings.Contains(ref.Message, core.CapabilitiesFor("forgejo").ThreadReplyReason()) {
		t.Errorf("message %q did not name the reason %q", ref.Message,
			core.CapabilitiesFor("forgejo").ThreadReplyReason())
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("unsupported host made %d call(s)", len(cmdr.calls))
	}
}
