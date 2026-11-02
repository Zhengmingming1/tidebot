package tasksvc

import "testing"

func TestNormalizeSkillComposition(t *testing.T) {
	got, err := NormalizeBindings([]SkillBinding{{SkillID: "a"}, {SkillID: "b"}})
	if err != nil || got[0].Role != "primary" || got[1].Role != "auxiliary" {
		t.Fatalf("legacy: %v %v", got, err)
	}
	got, err = NormalizeBindings([]SkillBinding{{SkillID: "a", Role: "auxiliary"}, {SkillID: "b", Role: "primary"}})
	if err != nil || got[0].SkillID != "b" || got[1].SkillID != "a" {
		t.Fatalf("main ordering: %v %v", got, err)
	}
	for _, bad := range [][]SkillBinding{
		{{SkillID: "a"}, {SkillID: "a"}},
		{{SkillID: "a", Role: "primary"}, {SkillID: "b", Role: "primary"}},
		{{SkillID: "a", Role: "auxiliary"}},
		{{SkillID: "../a"}},
		{{SkillID: "a", Mode: "pinned"}},
		{{SkillID: "a", Mode: "unknown"}},
	} {
		if _, err := NormalizeBindings(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	many := make([]SkillBinding, 17)
	if _, err := NormalizeBindings(many); err == nil {
		t.Fatal("unbounded composition")
	}
}
