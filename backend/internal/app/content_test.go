package app

import "testing"

func TestContentRejectsUnsafeButtonsAndUnknownTemplates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, action, kind, text string
		valid                    bool
	}{{"safe URL", "https://t.me/support", "url", "Hello", true}, {"javascript", "javascript:alert(1)", "url", "Hello", false}, {"unknown action", "grant:access", "callback", "Hello", false}, {"safe action", "club:join", "callback", "{{plan_name}}", true}, {"unknown template", "club:join", "callback", "{{secret}}", false}} {
		t.Run(tc.name, func(t *testing.T) {
			c := Content{Name: "Test", Text: tc.text, Buttons: []Button{{Label: "Open", Type: tc.kind, Action: tc.action}}}
			if e := validateContent(c); (e == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, e)
			}
		})
	}
}
func TestHTMLVariablesAreEscaped(t *testing.T) {
	t.Parallel()
	got := render("<b>{{plan_name}}</b>", map[string]string{"plan_name": "<script>&"}, true)
	if got != "<b>&lt;script&gt;&amp;</b>" {
		t.Fatal(got)
	}
}

func TestKeyboardRendersPriceInButtonLabel(t *testing.T) {
	t.Parallel()
	keyboard := renderKeyboard(
		[]Button{{Label: "Оплатить {{price}}", Type: "url", Action: "{{payment_url}}", Enabled: true}},
		map[string]string{"price": "12 USD", "payment_url": "https://pay.example/checkout"},
		false,
	)
	if len(keyboard) != 1 || len(keyboard[0]) != 1 || keyboard[0][0]["text"] != "Оплатить 12 USD" {
		t.Fatalf("unexpected keyboard: %#v", keyboard)
	}
}
