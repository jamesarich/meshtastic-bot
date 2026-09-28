package github

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
)

func TestFindSubmissionMatchesTheMarkerInTheTokensRecentIssues(t *testing.T) {
	var listed url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			w.Write([]byte(`{"login": "MeshtasticAutomation"}`))
		case "/repos/meshtastic/web/issues":
			listed = r.URL.Query()
			w.Write([]byte(`[
				{"number": 12, "html_url": "https://github.com/meshtastic/web/issues/12", "body": "other report"},
				{"number": 11, "html_url": "https://github.com/meshtastic/web/issues/11", "body": "answers\n\n<!-- meshtastic-bot submission abc -->"}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	gh := github.NewClient(nil)
	base, _ := url.Parse(srv.URL + "/")
	gh.BaseURL = base
	c := &LiveGitHubClient{client: gh, ctx: t.Context(), repoCache: map[string]*CachedRepository{}}

	since := time.Date(2026, 9, 28, 20, 0, 0, 0, time.UTC)
	got, err := c.FindSubmission("meshtastic", "web", "<!-- meshtastic-bot submission abc -->", since)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Number != 11 {
		t.Fatalf("found %+v, want #11", got)
	}
	if listed.Get("creator") != "MeshtasticAutomation" || listed.Get("state") != "all" || listed.Get("since") != "2026-09-28T20:00:00Z" {
		t.Errorf("listed with %v", listed)
	}

	if none, err := c.FindSubmission("meshtastic", "web", "<!-- meshtastic-bot submission zzz -->", since); err != nil || none != nil {
		t.Errorf("an unmatched marker found %+v, %v", none, err)
	}
}
