package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpoken(t *testing.T) {
	for in, want := range map[string]string{
		"[Skill – Fireball obtained!]":     "Skill – Fireball obtained!",
		"[1]":                              "",
		"He laughed.[12] Then he left.":    "He laughed. Then he left.",
		"*Thump.* The door shook.":         "Thump. The door shook.",
		"Salt & pepper, #3 and #hashtag":   "Salt and pepper, number 3 and hashtag",
		"Wait -- what... no":               "Wait — what… no",
		"Chapter XII":                      "Chapter 12",
		"Book iv and Part III":             "Book iv and Part 3",
		"The best part I ever played":      "The best part I ever played",
		"The book I read, the scene I saw": "The book I read, the scene I saw",
		"Book I'd say":                     "Book I'd say",
		"Chapter IV The Return":            "Chapter 4 The Return",
		"CHAPTER II: Home":                 "CHAPTER 2: Home",
		"the CIA, FBI and NSA; the US FBI agent, the BBC TV crew, the NHS, NATO": "the CIA, FBI and NSA; the US FBI agent, the BBC TV crew, the NHS, NATO",
		"“I KNOW!” she said. STOP!":                                              "I know! she said. Stop!",
		"The [Ah-na](/ɑnə/) & [Tom](/tɒm/) way":                                  "The [Ah-na](/ɑnə/) and [Tom](/tɒm/) way",
		"“Hello”—“Bye.” «Oui» „Ja“":                                              "Hello—Bye. Oui Ja",
		"Chapter MIM is no number":                                               "Chapter MIM is no number",
		"Part D and Part I":                                                      "Part D and Part 1",
		"“STOP RIGHT THERE,” he yelled.":                                         "Stop right there, he yelled.",
		"I SAID I WOULD.":                                                        "I said I would.",
		"NEVER! The FBI and NASA came. NO!":                                      "Never! The FBI and NASA came. NO!",
		"A ~decorated~ word, _really_ | there":                                   "A decorated word, really there",
		"<System> Level up":                                                      "System Level up",
		"Plain text stays.":                                                      "Plain text stays.",
		"GET OUT!":                                                               "Get out!",
		"WHO ARE YOU?":                                                           "Who are you?",
		"I’M NOT GOING":                                                          "I’m not going",
		"THE CIA DID IT!":                                                        "The CIA did it!",
		"See Chapter IV for details":                                             "See Chapter 4 for details",
		"Act I Scene II":                                                         "Act 1 Scene 2",
		"Chapter\u00a0XII":                                                       "Chapter 12",
		"‘I won’t,’ she said. ‘em":                                               "I won’t, she said. em",
		"Ryoka’s run, rock’n’roll":                                               "Ryoka’s run, rock’n’roll",
		"[Skill: [Ryoka](/ɹjˈOkə/)’s Run]":                                       "Skill: [Ryoka](/ɹjˈOkə/)’s Run",
	} {
		assert.Equal(t, want, spoken(in), in)
	}
}
