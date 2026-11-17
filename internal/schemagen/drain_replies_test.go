package schemagen

import (
	"testing"
)

const drainRepliesSchema = "drain-replies.schema.json"

func TestDrainRepliesReplyDefIsClosed(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name == drainRepliesSchema {
			if !replyDefIsClosed(t, file.Data) {
				t.Error("$defs.reply additionalProperties must be false")
			}
			return
		}
	}
	t.Fatal(drainRepliesSchema + " was not generated")
}

func replyDefIsClosed(t *testing.T, data []byte) bool {
	t.Helper()
	defs := schemaDefs(t, data, drainRepliesSchema)
	reply, ok := defs["reply"].(map[string]any)
	if !ok {
		t.Fatal(drainRepliesSchema + " has no $defs.reply")
	}
	return reply["additionalProperties"] == false
}
