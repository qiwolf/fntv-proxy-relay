package logger

import "testing"

func TestSafeURLDropsSecrets(t *testing.T) {
	for in, want := range map[string]string{"https://user:secret@cdn.test:443/token/secret?sign=secret#secret": "https://cdn.test:443", "http://[::1]:80/private": "http://[::1]:80", "not-a-url": "[redacted-url]", "https://x/%zz?password=secret": "[redacted-url]"} {
		if got := SafeURL(in); got != want {
			t.Fatalf("unsafe URL hint %q", got)
		}
	}
}
