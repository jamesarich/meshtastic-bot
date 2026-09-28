package github

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

const (
	// RepositoryCacheTTL defines how long repository metadata is cached
	RepositoryCacheTTL = 4 * time.Hour

	// apiTimeout bounds each call. Handlers hold cache locks across them, so an
	// unbounded hang would stall every /changelog and /repo behind it.
	apiTimeout = 10 * time.Second
)

type Client interface {
	GetReleases(owner, repo string, limit int) ([]*github.RepositoryRelease, error)
	CompareCommits(owner, repo, base, head string) (*github.CommitsComparison, error)
	CompareCommitsPage(owner, repo, base, head string, perPage, page int) (*github.CommitsComparison, error)
	CreateIssue(owner, repo, title, body string, labels []string) (*IssueResponse, error)
	GetRepository(owner, repo string) (*github.Repository, error)
}

type CachedRepository struct {
	Repository *github.Repository
	Timestamp  time.Time
}

type LiveGitHubClient struct {
	token     string
	client    *github.Client
	ctx       context.Context
	repoCache map[string]*CachedRepository
	cacheMux  sync.RWMutex
}

type IssueRequest struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

type IssueResponse struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	ID      int64  `json:"id"`
}

func NewClient(token string) Client {
	ctx := context.Background()

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)

	return &LiveGitHubClient{
		token:     token,
		client:    github.NewClient(tc),
		ctx:       ctx,
		repoCache: make(map[string]*CachedRepository),
	}
}

func (c *LiveGitHubClient) GetReleases(owner, repo string, limit int) ([]*github.RepositoryRelease, error) {
	opts := &github.ListOptions{
		PerPage: limit,
	}
	ctx, cancel := context.WithTimeout(c.ctx, apiTimeout)
	defer cancel()
	releases, _, err := c.client.Repositories.ListReleases(ctx, owner, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}
	return releases, nil
}

func (c *LiveGitHubClient) CompareCommits(owner, repo, base, head string) (*github.CommitsComparison, error) {
	ctx, cancel := context.WithTimeout(c.ctx, apiTimeout)
	defer cancel()
	comparison, resp, err := c.client.Repositories.CompareCommits(ctx, owner, repo, base, head, nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("github API returned %d: failed to compare commits: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("failed to compare commits: %w", err)
	}
	return comparison, nil
}

// CompareCommitsPage returns one page of a comparison's commits, oldest first.
// The unpaged comparison stops at 250 commits, and the newest are on the last page.
func (c *LiveGitHubClient) CompareCommitsPage(owner, repo, base, head string, perPage, page int) (*github.CommitsComparison, error) {
	ctx, cancel := context.WithTimeout(c.ctx, apiTimeout)
	defer cancel()
	comparison, _, err := c.client.Repositories.CompareCommits(ctx, owner, repo, base, head,
		&github.ListOptions{PerPage: perPage, Page: page})
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits (page %d): %w", page, err)
	}
	return comparison, nil
}

func (c *LiveGitHubClient) CreateIssue(owner, repo, title, body string, labels []string) (*IssueResponse, error) {
	log.Printf("[GitHub API] Creating issue in %s/%s", owner, repo)
	log.Printf("[GitHub API] Title: %s", title)
	log.Printf("[GitHub API] Labels: %v", labels)

	req := &github.IssueRequest{
		Title: github.String(title),
		Body:  github.String(body),
	}

	// go-github requires *string slices, so we adapt if labels exist
	if len(labels) > 0 {
		req.Labels = &labels
	}

	ctx, cancel := context.WithTimeout(c.ctx, apiTimeout)
	defer cancel()
	issue, resp, err := c.client.Issues.Create(ctx, owner, repo, req)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("github API returned %d: %w", resp.StatusCode, err)
		}
		return nil, err
	}

	return &IssueResponse{
		Number:  issue.GetNumber(),
		HTMLURL: issue.GetHTMLURL(),
		ID:      issue.GetID(),
	}, nil
}

func (c *LiveGitHubClient) GetRepository(owner, repo string) (*github.Repository, error) {
	cacheKey := fmt.Sprintf("%s/%s", owner, repo)

	// First check with read lock
	c.cacheMux.RLock()
	if cached, exists := c.repoCache[cacheKey]; exists {
		if time.Since(cached.Timestamp) < RepositoryCacheTTL {
			c.cacheMux.RUnlock()
			return cached.Repository, nil
		}
	}
	c.cacheMux.RUnlock()

	// Cache miss or expired - acquire write lock
	c.cacheMux.Lock()
	defer c.cacheMux.Unlock()

	// Double-check after acquiring write lock
	if cached, exists := c.repoCache[cacheKey]; exists {
		if time.Since(cached.Timestamp) < RepositoryCacheTTL {
			return cached.Repository, nil
		}
	}

	// Fetch from GitHub API
	ctx, cancel := context.WithTimeout(c.ctx, apiTimeout)
	defer cancel()
	repository, _, err := c.client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	// Store in cache with timestamp
	c.repoCache[cacheKey] = &CachedRepository{
		Repository: repository,
		Timestamp:  time.Now(),
	}

	return repository, nil
}

func FormatIssueBody(username, userID, description string) string {
	return fmt.Sprintf(`**Reported by:** %s (ID: %s)

%s

---
*This issue was automatically created from Discord*`, username, userID, description)
}
