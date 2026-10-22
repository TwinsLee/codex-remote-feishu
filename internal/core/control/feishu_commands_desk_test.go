package control

import "testing"

func TestDeskIsExplicitCommandAndKeepsDetachSeparate(t *testing.T) {
	for _, tc := range []struct {
		text, family string
		kind         ActionKind
	}{
		{"/desk", "desk", ActionKind("surface.button.desk")},
		{"/detach", "detach", ActionDetach},
	} {
		t.Run(tc.text, func(t *testing.T) {
			action, ok := ParseFeishuTextActionWithoutCatalog(tc.text)
			if !ok || action.CommandID != tc.family || action.Kind != tc.kind {
				t.Fatalf("command did not resolve to its own action: %#v, %t", action, ok)
			}
		})
	}
	if _, ok := ParseFeishuTextActionWithoutCatalog("/desk force"); ok {
		t.Fatal("/desk must not expose an implicit force operation")
	}
}
