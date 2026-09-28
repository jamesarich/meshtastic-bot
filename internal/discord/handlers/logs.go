package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// logFileLimit is how much of one attached file is read.
	logFileLimit = 256 << 10
	// logBodyBudget bounds the logs in one issue; GitHub rejects a body over
	// 65536 characters, and the answers need room too.
	logBodyBudget = 40000
)

var logHTTP = &http.Client{Timeout: 10 * time.Second}

// logFile is an attachment as the issue shows it: its text, or a note saying
// why it was left out.
type logFile struct {
	Name    string
	Content string
	Note    string
}

var textExtensions = map[string]bool{".txt": true, ".log": true, ".json": true, ".csv": true, ".yaml": true, ".yml": true}

func isTextFile(f AttachedFile) bool {
	ct := strings.ToLower(f.ContentType)
	return strings.HasPrefix(ct, "text/") || strings.HasPrefix(ct, "application/json") ||
		textExtensions[strings.ToLower(path.Ext(f.Name))]
}

// fetchLogs downloads the reporter's text attachments. Discord's attachment
// links expire, so the text itself goes in the issue rather than a link.
func fetchLogs(files []AttachedFile) []logFile {
	out := make([]logFile, 0, len(files))
	budget := logBodyBudget
	for _, f := range files {
		entry := logFile{Name: f.Name}
		switch {
		case !isTextFile(f):
			entry.Note = "not included; only text files can be attached from Discord, so add it in a comment"
		case budget <= 0:
			entry.Note = "not included; the issue has no room for more logs"
		default:
			text, truncated, err := downloadText(f.URL)
			if err != nil {
				log.Printf("Could not download an attached log: %v", err)
				entry.Note = "could not be downloaded"
				break
			}
			if r := []rune(text); len(r) > budget {
				text, truncated = string(r[:budget]), true
			}
			budget -= utf8.RuneCountInString(text)
			entry.Content = text
			if truncated {
				entry.Note = "truncated"
			}
		}
		out = append(out, entry)
	}
	return out
}

func downloadText(url string) (text string, truncated bool, err error) {
	resp, err := logHTTP.Get(url)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, logFileLimit+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > logFileLimit {
		data, truncated = data[:logFileLimit], true
	}
	if !utf8.Valid(data) {
		// A cut can split the last character; anything else is not text.
		trimmed := []byte(strings.ToValidUTF8(string(data), ""))
		if !truncated || len(data)-len(trimmed) > utf8.UTFMax {
			return "", false, fmt.Errorf("not UTF-8 text")
		}
		data = trimmed
	}
	return string(data), truncated, nil
}
