package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// The client library reports a failure two ways: an APIError when it could
// parse the provider's JSON error body, and a bare RequestError when it could
// not. A rejected API key produces the second, because a provider that will
// not talk to you has nothing to say about your request.
//
// Only APIError was classified, so a 403 skipped the auth branch entirely and
// reached the operator as "llm: upstream error: error, status code: 403,
// message:" — the provider's empty body, verbatim. The one actionable fact,
// that the configured key is not accepted, was the one thing missing.
func TestARejectedKeyIsNamedAsSuch(t *testing.T) {
	c := &Client{}

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			for name, raw := range map[string]error{
				"parsed body":   &openai.APIError{HTTPStatusCode: status, Message: ""},
				"unparsed body": &openai.RequestError{HTTPStatusCode: status, Err: errors.New("")},
			} {
				got := c.classifyError(raw)
				if !errors.Is(got, ErrUpstream) {
					t.Fatalf("%s: classifyError() = %v, want ErrUpstream", name, got)
				}
				if !strings.Contains(got.Error(), "OPENROUTER_API_KEY") {
					t.Fatalf("%s: %q does not name the setting the operator has to change", name, got)
				}
			}
		})
	}
}

// Rate limiting and server errors keep their own classification, because the
// service layer applies different retry and circuit-breaker policy to each.
func TestOtherStatusesKeepTheirClassification(t *testing.T) {
	c := &Client{}

	if got := c.classifyError(&openai.APIError{HTTPStatusCode: http.StatusTooManyRequests}); !errors.Is(got, ErrRateLimited) {
		t.Errorf("429 classified as %v, want ErrRateLimited", got)
	}
	if got := c.classifyError(&openai.RequestError{HTTPStatusCode: http.StatusTooManyRequests, Err: errors.New("")}); !errors.Is(got, ErrRateLimited) {
		t.Errorf("429 without a parsed body classified as %v, want ErrRateLimited", got)
	}
	got := c.classifyError(&openai.APIError{HTTPStatusCode: http.StatusBadGateway, Message: "upstream down"})
	if !errors.Is(got, ErrUpstream) || strings.Contains(got.Error(), "OPENROUTER_API_KEY") {
		t.Errorf("502 classified as %v; a server error is not an auth problem", got)
	}
}

// The nesting is what the first attempt at this got wrong. When the provider
// sends a body the library can parse but not use, it returns a RequestError
// wrapping an APIError, and only the outer error carries the HTTP status — the
// inner one keeps its zero value.
//
// A switch that stopped at the first errors.As match found the inner APIError,
// read a status of 0, and fell through to the generic branch. The tests that
// went with it passed, because they built the two shapes independently and
// never nested them, so a real 403 kept printing as the provider's empty
// message while the suite was green.
func TestAStatusOnTheOuterErrorIsNotLostToTheInnerOne(t *testing.T) {
	nested := &openai.RequestError{
		HTTPStatusCode: http.StatusForbidden,
		Err:            &openai.APIError{}, // no status: this is where the zero came from
	}

	got := (&Client{}).classifyError(nested)

	if !strings.Contains(got.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("classifyError() = %q; the 403 on the outer error was not read", got)
	}
}

// The reverse nesting, and the plain cases, keep working: a status anywhere in
// the chain is enough.
func TestAStatusIsFoundWhereverItSits(t *testing.T) {
	cases := map[string]error{
		"outer request error":         &openai.RequestError{HTTPStatusCode: 401, Err: &openai.APIError{}},
		"inner api error carries it":  &openai.RequestError{Err: &openai.APIError{HTTPStatusCode: 401}},
		"api error alone":             &openai.APIError{HTTPStatusCode: 401},
		"request error alone":         &openai.RequestError{HTTPStatusCode: 401, Err: errors.New("EOF")},
		"wrapped by a caller with %w": fmt.Errorf("generate: %w", &openai.RequestError{HTTPStatusCode: 403, Err: &openai.APIError{}}),
	}

	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (&Client{}).classifyError(err); !strings.Contains(got.Error(), "OPENROUTER_API_KEY") {
				t.Fatalf("classifyError() = %q, want the rejected-key message", got)
			}
		})
	}
}
