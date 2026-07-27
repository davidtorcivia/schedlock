package server

import (
	"os"
	"testing"
)

// TestDistributedSkillMatchesServedSkill keeps the two copies of the agent
// instructions identical.
//
// One copy is embedded in the binary and served at /SKILL.md; the other ships in
// skills/ for agent tooling that loads it from disk. They had already drifted,
// which meant an agent could be working from documentation the server did not
// implement.
func TestDistributedSkillMatchesServedSkill(t *testing.T) {
	const distributed = "../../skills/calendar-proxy/SKILL.md"

	onDisk, err := os.ReadFile(distributed)
	if err != nil {
		t.Fatalf("failed to read %s: %v", distributed, err)
	}

	if string(onDisk) != string(skillMD) {
		t.Errorf("%s differs from the embedded internal/server/SKILL.md.\n"+
			"Copy the embedded file over it so agents and the server agree.", distributed)
	}
}
