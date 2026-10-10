package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSkillContextHashesLoadedBodySeparatelyFromEnvelope(t *testing.T) {
	c, _, scope, _, _ := skillContextHarness(t)
	out, err := c.ReadSkillContext(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("PRIVATE_SKILL_BODY"))
	load := out.Frames[0].Loads[0]
	if load.BodySHA256 != hex.EncodeToString(want[:]) || load.BodySHA256 == load.ContentSHA256 {
		t.Fatalf("body hash does not identify file bytes: %+v", load)
	}
}
