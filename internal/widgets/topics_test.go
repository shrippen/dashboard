package widgets

import "testing"

// Every type names its gallery topic, so new ones don't silently land in
// the overview.
func TestEveryTypeHasTopic(t *testing.T) {
	for _, kind := range AllTypes() {
		if _, ok := topicOf[kind.Key]; !ok {
			t.Errorf("%s has no topic", kind.Key)
		}
	}
}
