package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpoken(t *testing.T) {
	for in, want := range map[string]string{
		"[Skill – Fireball obtained!]":         "Skill – Fireball obtained!",
		"[1]":                                  "",
		"He laughed.[12] Then he left.":        "He laughed. Then he left.",
		"*Thump.* The door shook.":             "Thump. The door shook.",
		"Salt & pepper, #3 and #hashtag":       "Salt and pepper, number 3 and hashtag",
		"Wait -- what... no":                   "Wait — what… no",
		"Chapter XII":                          "Chapter 12",
		"Book iv and Part III":                 "Book iv and Part 3",
		"Chapter MIM is no number":             "Chapter MIM is no number",
		"Part D and Part I":                    "Part D and Part 1",
		"“STOP RIGHT THERE,” he yelled.":       "“Stop right there,” he yelled.",
		"I SAID I WOULD.":                      "I said I would.",
		"NEVER! The FBI and NASA came. NO!":    "Never! The FBI and NASA came. NO!",
		"A ~decorated~ word, _really_ | there": "A decorated word, really there",
		"<System> Level up":                    "System Level up",
		"Plain text stays.":                    "Plain text stays.",
	} {
		assert.Equal(t, want, spoken(in), in)
	}
}
