package epweave

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"strings"
)

var (
	ErrPadNotFound = errors.New("no pad with that title")
	ErrPadExists   = errors.New("a pad with that title already exists; use append")
)

// PrefixError reports a write outside the project's title prefix.
type PrefixError struct {
	Title  string
	Prefix string
}

func (e PrefixError) Error() string {
	return fmt.Sprintf("title %q is outside the writable prefix %q", e.Title, e.Prefix)
}

// Notes is the operation set the gateway exposes to the VM: read anything,
// write only under the project prefix, never delete or replace wholesale.
type Notes struct {
	client   *Client
	authorID string
	prefix   string
}

type ReadResult struct {
	PadID     string `json:"pad_id"`
	Revisions int    `json:"revisions"`
	Content   string `json:"content"`
}

type WriteResult struct {
	PadID     string `json:"pad_id"`
	URL       string `json:"url"`
	Revisions int    `json:"revisions"`
}

// ChangesResult always carries the lines added between the two revisions;
// the full diff HTML is included only on request because it runs to tens of
// kilobytes with styling the agent rarely needs.
type ChangesResult struct {
	PadID      string            `json:"pad_id"`
	FromRev    int               `json:"from_rev"`
	ToRev      int               `json:"to_rev"`
	Authors    map[string]string `json:"authors"`
	AddedLines []string          `json:"added_lines"`
	HTML       string            `json:"html,omitempty"`
}

// Connect obtains a token and the Etherpad author the VM's writes are attributed to.
func Connect(host HostConfig, project ProjectConfig, authorMapper, authorName string) (*Notes, error) {
	client, err := NewClient(host)
	if err != nil {
		return nil, err
	}
	authorID, err := client.CreateAuthorIfNotExistsFor(authorMapper, authorName)
	if err != nil {
		return nil, err
	}
	return &Notes{client: client, authorID: authorID, prefix: project.TitlePrefix()}, nil
}

func (n *Notes) URL() string {
	return n.client.URL
}

func (n *Notes) Prefix() string {
	return n.prefix
}

func (n *Notes) padURL(padID string) string {
	return n.client.URL + "/p/" + padID
}

func (n *Notes) Search(query string, rows int) ([]SearchDoc, error) {
	return n.client.Search(query, rows)
}

func (n *Notes) Read(title string, asHTML bool) (ReadResult, error) {
	padID, err := n.lookup(title)
	if err != nil {
		return ReadResult{}, err
	}
	revisions, err := n.client.RevisionsCount(padID)
	if err != nil {
		return ReadResult{}, err
	}
	var content string
	if asHTML {
		content, err = n.client.HTML(padID)
	} else {
		content, err = n.client.Text(padID)
	}
	if err != nil {
		return ReadResult{}, err
	}
	return ReadResult{PadID: padID, Revisions: revisions, Content: content}, nil
}

func (n *Notes) Create(title, markdown, tags string) (WriteResult, error) {
	if err := n.checkPrefix(title); err != nil {
		return WriteResult{}, err
	}
	if _, found, err := n.client.PadIDByTitle(title); err != nil {
		return WriteResult{}, err
	} else if found {
		return WriteResult{}, fmt.Errorf("%s: %w", title, ErrPadExists)
	}
	fragment := fmt.Sprintf("<h1>%s</h1>", html.EscapeString(title)) + MarkdownToHTML(markdown)
	if tags != "" {
		fragment += "<p>" + html.EscapeString(tags) + "</p>"
	}
	padID := newPadID()
	if err := n.client.CreatePad(padID, n.authorID); err != nil {
		return WriteResult{}, err
	}
	if err := n.client.SetHTML(padID, htmlDocument(fragment), n.authorID); err != nil {
		return WriteResult{}, err
	}
	return n.writeResult(padID)
}

func (n *Notes) writeResult(padID string) (WriteResult, error) {
	revisions, err := n.client.RevisionsCount(padID)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{PadID: padID, URL: n.padURL(padID), Revisions: revisions}, nil
}

func (n *Notes) Append(title, markdown string) (WriteResult, error) {
	if err := n.checkPrefix(title); err != nil {
		return WriteResult{}, err
	}
	padID, err := n.lookup(title)
	if err != nil {
		return WriteResult{}, err
	}
	current, err := n.client.HTML(padID)
	if err != nil {
		return WriteResult{}, err
	}
	at, err := insertionPoint(current)
	if err != nil {
		return WriteResult{}, err
	}
	updated := current[:at] + MarkdownToHTML(markdown) + current[at:]
	if err := n.client.SetHTML(padID, updated, n.authorID); err != nil {
		return WriteResult{}, err
	}
	return n.writeResult(padID)
}

func (n *Notes) Changes(title string, since int, includeHTML bool) (ChangesResult, error) {
	padID, err := n.lookup(title)
	if err != nil {
		return ChangesResult{}, err
	}
	revisions, err := n.client.RevisionsCount(padID)
	if err != nil {
		return ChangesResult{}, err
	}
	result := ChangesResult{PadID: padID, FromRev: since, ToRev: revisions, Authors: map[string]string{}, AddedLines: []string{}}
	if since >= revisions {
		return result, nil
	}
	before, err := n.client.HTMLAt(padID, since)
	if err != nil {
		return ChangesResult{}, err
	}
	after, err := n.client.HTML(padID)
	if err != nil {
		return ChangesResult{}, err
	}
	result.AddedLines = addedLines(htmlLines(before), htmlLines(after))
	diff, authors, err := n.client.DiffHTML(padID, since, revisions)
	if err != nil {
		return ChangesResult{}, err
	}
	if includeHTML {
		result.HTML = diff
	}
	for _, authorID := range authors {
		name, err := n.client.AuthorName(authorID)
		if err != nil {
			return ChangesResult{}, err
		}
		result.Authors[authorID] = name
	}
	return result, nil
}

func (n *Notes) lookup(title string) (string, error) {
	padID, found, err := n.client.PadIDByTitle(title)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%s: %w", title, ErrPadNotFound)
	}
	return padID, nil
}

func (n *Notes) checkPrefix(title string) error {
	if !strings.HasPrefix(title, n.prefix) || len(title) == len(n.prefix) {
		return PrefixError{Title: title, Prefix: n.prefix}
	}
	return nil
}

// newPadID returns a random UUID-shaped id, matching the pads ep_weave creates itself.
func newPadID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
