package skillstore

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Details are frozen with each package version, including its acceptance examples.
type SkillSetDetails struct {
	Description  string     `json:"description,omitempty"`
	Instructions string     `json:"instructions,omitempty"`
	InputHint    string     `json:"input_hint,omitempty"`
	OutputHint   string     `json:"output_hint,omitempty"`
	TestCases    []EvalCase `json:"test_cases,omitempty"`
}
type EvalCase struct {
	Name           string   `json:"name"`
	Input          string   `json:"input"`
	MustContain    []string `json:"must_contain,omitempty"`
	MustNotContain []string `json:"must_not_contain,omitempty"`
}

func ValidateSkillSetDetails(d SkillSetDetails) error {
	if len(d.Description) > 4096 || len(d.Instructions) > 8192 || len(d.InputHint) > 8192 || len(d.OutputHint) > 8192 {
		return errors.New("package description allows 4 KiB; instructions and input/output hints allow 8 KiB each")
	}
	if len(d.TestCases) > 5 {
		return errors.New("a package allows at most five test cases")
	}
	for i, c := range d.TestCases {
		if strings.TrimSpace(c.Name) == "" || utf8.RuneCountInString(c.Name) > 80 {
			return fmt.Errorf("test case %d needs a name (max 80 characters)", i+1)
		}
		if strings.TrimSpace(c.Input) == "" || len(c.Input) > 8192 {
			return fmt.Errorf("test case %d needs an input (max 8 KiB)", i+1)
		}
		if len(c.MustContain)+len(c.MustNotContain) == 0 || len(c.MustContain)+len(c.MustNotContain) > 8 {
			return fmt.Errorf("test case %d needs 1–8 assertions", i+1)
		}
		for _, list := range [][]string{c.MustContain, c.MustNotContain} {
			for _, a := range list {
				if strings.TrimSpace(a) == "" || len(a) > 1024 {
					return fmt.Errorf("test case %d assertions must be non-empty (max 1 KiB each)", i+1)
				}
			}
		}
		for _, a := range c.MustContain {
			for _, b := range c.MustNotContain {
				if strings.Contains(a, b) {
					return fmt.Errorf("test case %d has contradictory assertions", i+1)
				}
			}
		}
	}
	return nil
}
