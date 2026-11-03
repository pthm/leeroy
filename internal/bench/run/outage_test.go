package run

import "testing"

// Provider errors seen in real runs, sorted into outages to wait out and errors that won't clear by waiting.
func TestTransientError(t *testing.T) {
	for why, want := range map[string]bool{
		"Selected model is at capacity. Please try a different model.": true,
		"Overloaded": true,
		"API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}": true,
		"rate limit reached for requests":                                               true,
		"stream disconnected before completion: error sending request":                  true,
		"503 Service Unavailable":                                                       true,
		"You've hit your usage limit. Upgrade to Pro or try again later.":               false,
		"insufficient_quota":                                                            false,
		"Invalid API key · Please run /login":                                           false,
		"401 Unauthorized":                                                              false,
		"":                                                                              false,
		"the model said something odd":                                                  false,
	} {
		if got := transientError(why); got != want {
			t.Errorf("transientError(%q) = %v, want %v", why, got, want)
		}
	}
}

func TestOutageCode(t *testing.T) {
	for why, want := range map[string]string{
		"API Error: 529 Overloaded": "capacity", "429 Too Many Requests": "rate_limited",
		"request timed out": "timeout", "stream disconnected": "network", "500 Internal Server Error": "server_error",
	} {
		if got := outageCode(why); got != want {
			t.Errorf("%q: %s, want %s", why, got, want)
		}
	}
}
