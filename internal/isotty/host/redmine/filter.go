package redmine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
	"github.com/zricethezav/gitleaks/v8/detect"
)

var (
	emailPattern = regexp.MustCompile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)
	// Japanese phone numbers as people write them: three groups split by
	// hyphens, spaces or parentheses (03-1234-5678, 090 1234 5678,
	// 090(1234)5678, (03)1234-5678), ten or eleven digits without
	// separators (0312345678, 09012345678), or the +81 form. The digits
	// around a match are checked by hand, since RE2 has no look-around.
	phonePattern = regexp.MustCompile(
		`\(0\d{1,4}\)[-－ ]?\d{1,4}[-－ ]\d{3,4}` +
			`|0\d{1,4}(?:[-－ ]\(?|\()\d{1,4}(?:\)[-－ ]?|[-－ ])\d{3,4}` +
			`|0\d{9,10}` +
			`|\+81[-－ ]?(?:\(0\))?\d{1,4}[-－ ]?\d{1,4}[-－ ]?\d{3,4}`)
)

// personKeys are the JSON keys under which Redmine puts a person as
// {id, name} in the answers the relay serves, and otherKeys those under
// which an {id, name} is not a person (the relay's own status lists its
// projects). An {id, name} under any other key fails the answer, so a
// person in a field added by a later Redmine or a plugin is not let
// through as text.
var (
	personKeys = map[string]bool{"author": true, "assigned_to": true, "user": true, "watchers": true, "updated_by": true, "default_assignee": true}
	otherKeys  = map[string]bool{
		"project": true, "projects": true, "parent": true, "tracker": true, "trackers": true,
		"status": true, "allowed_statuses": true, "priority": true, "category": true,
		"issue_categories": true, "fixed_version": true, "default_version": true,
		"custom_fields": true, "issue_custom_fields": true, "time_entry_activities": true,
		"enabled_modules": true,
	}
)

// What the filter puts in place of what it hides.
const (
	emailMark  = "[email]"
	phoneMark  = "[phone]"
	secretMark = "[secret]"
	nameMark   = "[name]"
)

// Mark is one replacement in what the VM receives: the field, as a JSON
// path, and the characters of the replacement in its filtered text.
type Mark struct {
	Field string `json:"field"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Kind  string `json:"kind"`
}

// Filter hides contact details and secrets by pattern, gives each person a
// Redmine answer names a pseudonym of their user id, and hides the names
// morphological analysis finds in text. See docs/REDMINE.md.
type Filter struct {
	salt      []byte
	tokenizer *tokenizer.Tokenizer
	secrets   *detect.Detector
}

func NewFilter(salt []byte) (*Filter, error) {
	t, err := tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())
	if err != nil {
		return nil, fmt.Errorf("load the morphological dictionary: %w", err)
	}
	secrets, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("load the gitleaks rules: %w", err)
	}
	return &Filter{salt: salt, tokenizer: t, secrets: secrets}, nil
}

// pseudonym is the person with the Redmine user id: six hex digits of a
// keyed hash, the same for the same id under the project's salt.
func (f *Filter) pseudonym(id int) string {
	mac := hmac.New(sha256.New, f.salt)
	mac.Write([]byte(strconv.Itoa(id)))
	return "Person-" + hex.EncodeToString(mac.Sum(nil))[:6]
}

// JSON filters a whole Redmine answer.
func (f *Filter) JSON(data []byte) ([]byte, []Mark, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, nil, fmt.Errorf("the Redmine answered something that is not JSON: %w", err)
	}
	var marks []Mark
	filtered, err := f.walk(value, "", "", &marks)
	if err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(filtered)
	if err != nil {
		return nil, nil, err
	}
	return out, marks, nil
}

// walk filters the value at path; key is the JSON key it sits under, which
// says whether an object is a person.
func (f *Filter) walk(value any, key, path string, marks *[]Mark) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		_, hasID := v["id"]
		_, hasName := v["name"]
		if hasID && hasName && !personKeys[key] && !otherKeys[key] {
			return nil, fmt.Errorf("%s holds an {id, name} the filter does not know as a person or not; the answer is withheld until the filter knows it", path)
		}
		if _, named := v["name"].(string); personKeys[key] && named {
			id, ok := v["id"].(float64)
			if !ok || id != float64(int(id)) || id <= 0 {
				return nil, fmt.Errorf("%s names a person without a user id", path)
			}
			alias := f.pseudonym(int(id))
			v["name"] = alias
			delete(v, "id")
			*marks = append(*marks, Mark{Field: path + ".name", Start: 0, End: utf8.RuneCountInString(alias), Kind: "person"})
			return v, nil
		}
		if v["property"] == "attr" && userAttributes[v["name"]] {
			for _, k := range []string{"old_value", "new_value"} {
				if err := f.pseudonymValue(v, k, path+"."+k, marks); err != nil {
					return nil, err
				}
			}
			return v, nil
		}
		for k, child := range v {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			filtered, err := f.walk(child, k, childPath, marks)
			if err != nil {
				return nil, err
			}
			v[k] = filtered
		}
		return v, nil
	case []any:
		for i, child := range v {
			filtered, err := f.walk(child, key, fmt.Sprintf("%s[%d]", path, i), marks)
			if err != nil {
				return nil, err
			}
			v[i] = filtered
		}
		return v, nil
	case string:
		text, found := f.Text(v)
		for _, m := range found {
			m.Field = path
			*marks = append(*marks, m)
		}
		return text, nil
	default:
		return value, nil
	}
}

// userAttributes are the issue attributes a journal detail records as a
// user id, before and after the change.
var userAttributes = map[any]bool{"assigned_to_id": true, "author_id": true}

// pseudonymValue puts the pseudonym of the user id at key in place of the
// id; a change from or to nobody leaves the value empty or absent.
func (f *Filter) pseudonymValue(detail map[string]any, key, path string, marks *[]Mark) error {
	raw, present := detail[key]
	if !present || raw == nil || raw == "" {
		return nil
	}
	text, ok := raw.(string)
	id, err := strconv.Atoi(text)
	if !ok || err != nil || id <= 0 {
		return fmt.Errorf("%s is not a user id", path)
	}
	alias := f.pseudonym(id)
	detail[key] = alias
	*marks = append(*marks, Mark{Field: path, Start: 0, End: utf8.RuneCountInString(alias), Kind: "person"})
	return nil
}

// hit is a range of the text, in bytes, and what is there.
type hit struct {
	start, end int
	kind       string
}

// Text filters one text: contact details and secrets by pattern first,
// then the names morphological analysis finds. The marks have no field.
func (f *Filter) Text(text string) (string, []Mark) {
	var done []hit
	text, done = replace(text, done, f.contacts(text))
	text, done = replace(text, done, f.secretHits(text))
	text, done = replace(text, done, f.names(text))
	// done is in the text's order, so one walk counts the characters.
	marks := make([]Mark, len(done))
	pos, chars := 0, 0
	for i, h := range done {
		chars += utf8.RuneCountInString(text[pos:h.start])
		start := chars
		chars += utf8.RuneCountInString(text[h.start:h.end])
		pos = h.end
		marks[i] = Mark{Start: start, End: chars, Kind: h.kind}
	}
	return text, marks
}

func (f *Filter) contacts(text string) []hit {
	var found []hit
	for _, loc := range emailPattern.FindAllStringIndex(text, -1) {
		found = append(found, hit{loc[0], loc[1], "email"})
	}
	for _, loc := range phonePattern.FindAllStringIndex(text, -1) {
		if !digitBefore(text, loc[0]) && !digitAfter(text, loc[1]) {
			found = append(found, hit{loc[0], loc[1], "phone"})
		}
	}
	return found
}

func digitBefore(text string, i int) bool {
	return i > 0 && text[i-1] >= '0' && text[i-1] <= '9'
}

func digitAfter(text string, i int) bool {
	return i < len(text) && text[i] >= '0' && text[i] <= '9'
}

// secretHits finds every place of each secret gitleaks reports; a secret
// reported more than once is looked for once.
func (f *Filter) secretHits(text string) []hit {
	var found []hit
	seen := map[string]bool{}
	for _, finding := range f.secrets.DetectString(text) {
		if seen[finding.Secret] {
			continue
		}
		seen[finding.Secret] = true
		for from := 0; ; {
			i := strings.Index(text[from:], finding.Secret)
			if finding.Secret == "" || i < 0 {
				break
			}
			found = append(found, hit{from + i, from + i + len(finding.Secret), "secret"})
			from += i + len(finding.Secret)
		}
	}
	return found
}

// names finds the words the IPA dictionary tags as person names.
func (f *Filter) names(text string) []hit {
	var found []hit
	for _, token := range f.tokenizer.Tokenize(text) {
		pos := token.POS()
		if len(pos) > 2 && pos[0] == "名詞" && pos[1] == "固有名詞" && pos[2] == "人名" {
			found = append(found, hit{token.Position, token.Position + len(token.Surface), "name"})
		}
	}
	return found
}

var markOf = map[string]string{"email": emailMark, "phone": phoneMark, "secret": secretMark, "name": nameMark}

// replace puts each kind's mark in place of the new hits that touch
// neither an earlier replacement nor each other, and returns the text with
// every replacement so far, at its place in that text. done is in the
// text's order and found is sorted into it, so both are walked once.
func replace(text string, done, found []hit) (string, []hit) {
	sort.Slice(found, func(i, j int) bool { return found[i].start < found[j].start })
	var out strings.Builder
	var all []hit
	last, d, next := 0, 0, 0
	for _, h := range found {
		for next < len(done) && done[next].end <= h.start {
			next++
		}
		if h.start < last || next < len(done) && done[next].start < h.end {
			continue
		}
		for ; d < len(done) && done[d].start < h.start; d++ {
			all = append(all, moved(done[d], out.Len()+done[d].start-last))
		}
		out.WriteString(text[last:h.start])
		mark := markOf[h.kind]
		all = append(all, hit{out.Len(), out.Len() + len(mark), h.kind})
		out.WriteString(mark)
		last = h.end
	}
	for ; d < len(done); d++ {
		all = append(all, moved(done[d], out.Len()+done[d].start-last))
	}
	out.WriteString(text[last:])
	return out.String(), all
}

func moved(h hit, start int) hit {
	return hit{start, start + h.end - h.start, h.kind}
}
