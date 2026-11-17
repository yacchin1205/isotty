package redmine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// maxAttachment bounds what is downloaded for one attachment.
const maxAttachment = 50 << 20

// issueListParams are the query parameters the VM may pass to the issue
// list; project_id and subproject_id are the relay's.
var issueListParams = map[string]bool{"status_id": true, "tracker_id": true, "sort": true, "limit": true, "offset": true}

// relay serves the pinned projects, every answer filtered and recorded.
type relay struct {
	api      *api
	cfg      HostConfig
	projects []Project
	filter   *Filter
	record   func(Delivery) error
}

// Delivery is what the relay hands the VM for one read: the read as the
// VM asked for it, the filtered content and where the filter replaced
// something in it.
type Delivery struct {
	Target  string
	Content string
	Marks   []Mark
}

// Handler reads the pinned projects with the host key and serves the
// reads; the host gateway mounts it under /redmine. It fails when a pinned
// project cannot be read, so attach says so. Every answer is passed to
// record before the VM gets it, and withheld when that fails.
func Handler(cfg HostConfig, pins ProjectConfig, salt []byte, record func(Delivery) error) (http.Handler, error) {
	filter, err := NewFilter(salt)
	if err != nil {
		return nil, err
	}
	r := &relay{api: newAPI(cfg), cfg: cfg, filter: filter, record: record}
	for _, identifier := range pins.Projects {
		project, err := r.api.project(identifier)
		if err != nil {
			return nil, err
		}
		r.projects = append(r.projects, project)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", r.status)
	mux.HandleFunc("GET /projects/{identifier}", r.project)
	mux.HandleFunc("GET /projects/{identifier}/issues", r.issues)
	mux.HandleFunc("GET /issues/{id}", r.issue)
	mux.HandleFunc("GET /issues/{id}/attachments/{aid}/download", r.issueAttachment)
	mux.HandleFunc("GET /projects/{identifier}/wiki", r.wikiIndex)
	mux.HandleFunc("GET /projects/{identifier}/wiki/{title}", r.wikiPage)
	mux.HandleFunc("GET /projects/{identifier}/wiki/{title}/attachments/{aid}/download", r.wikiAttachment)
	return mux, nil
}

func (r *relay) status(w http.ResponseWriter, req *http.Request) {
	body, err := json.Marshal(map[string]any{"url": r.cfg.URL, "projects": r.projects})
	if err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filtered, marks, err := r.filter.JSON(body)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	r.deliver(w, req, "application/json", string(filtered), marks)
}

// pinned returns the pinned project named in the path, or answers 403.
func (r *relay) pinned(w http.ResponseWriter, identifier string) (Project, bool) {
	for _, project := range r.projects {
		if project.Identifier == identifier {
			return project, true
		}
	}
	gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("project %s is not among the projects pinned for this VM (%s); ask the human to run `isotty host redmine add --project %s` on the host", identifier, r.identifiers(), identifier))
	return Project{}, false
}

func (r *relay) identifiers() string {
	names := make([]string, len(r.projects))
	for i, project := range r.projects {
		names[i] = project.Identifier
	}
	return strings.Join(names, ", ")
}

// fetch reads a JSON path from the Redmine and answers the VM with it,
// filtered. check sees the raw answer first and may refuse it.
func (r *relay) fetch(w http.ResponseWriter, req *http.Request, path string, params url.Values, check func([]byte) error) {
	body, status, err := r.api.get(path, params)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the Redmine could not be reached: "+err.Error())
		return
	}
	if status != http.StatusOK {
		gateway.WriteError(w, status, fmt.Sprintf("the Redmine answered %d", status))
		return
	}
	if check != nil {
		if err := check(body); err != nil {
			gateway.WriteError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	filtered, marks, err := r.filter.JSON(body)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	r.deliver(w, req, "application/json", string(filtered), marks)
}

// deliver records the answer and only then gives it to the VM.
func (r *relay) deliver(w http.ResponseWriter, req *http.Request, contentType, content string, marks []Mark) {
	if err := r.record(Delivery{Target: req.URL.RequestURI(), Content: content, Marks: marks}); err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the answer is withheld because its audit record could not be written: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write([]byte(content))
}

func (r *relay) project(w http.ResponseWriter, req *http.Request) {
	project, ok := r.pinned(w, req.PathValue("identifier"))
	if !ok {
		return
	}
	r.fetch(w, req, "/projects/"+url.PathEscape(project.Identifier)+".json", nil, nil)
}

func (r *relay) issues(w http.ResponseWriter, req *http.Request) {
	project, ok := r.pinned(w, req.PathValue("identifier"))
	if !ok {
		return
	}
	params := url.Values{"project_id": {project.Identifier}, "subproject_id": {"!*"}}
	for key, values := range req.URL.Query() {
		if !issueListParams[key] {
			gateway.WriteError(w, http.StatusBadRequest, fmt.Sprintf("query parameter %s is not accepted; the list takes status_id, tracker_id, sort, limit and offset", key))
			return
		}
		params[key] = values
	}
	r.fetch(w, req, "/issues.json", params, nil)
}

// issueProject reads which project an issue belongs to and refuses one
// outside the pin; the issue is numbered across the whole Redmine.
func (r *relay) issueProject(body []byte) error {
	var answer struct {
		Issue struct {
			ID      int `json:"id"`
			Project struct {
				ID int `json:"id"`
			} `json:"project"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("the Redmine answered something that is not an issue: %w", err)
	}
	for _, project := range r.projects {
		if project.ID == answer.Issue.Project.ID {
			return nil
		}
	}
	return fmt.Errorf("issue #%d belongs to a project that is not pinned for this VM (%s)", answer.Issue.ID, r.identifiers())
}

func (r *relay) issue(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.Atoi(req.PathValue("id"))
	if err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "the issue id is a number")
		return
	}
	r.fetch(w, req, "/issues/"+strconv.Itoa(id)+".json", url.Values{"include": {"children,relations,journals,attachments,watchers"}}, r.issueProject)
}

func (r *relay) wikiIndex(w http.ResponseWriter, req *http.Request) {
	project, ok := r.pinned(w, req.PathValue("identifier"))
	if !ok {
		return
	}
	r.fetch(w, req, "/projects/"+url.PathEscape(project.Identifier)+"/wiki/index.json", nil, nil)
}

func (r *relay) wikiPage(w http.ResponseWriter, req *http.Request) {
	project, ok := r.pinned(w, req.PathValue("identifier"))
	if !ok {
		return
	}
	r.fetch(w, req, "/projects/"+url.PathEscape(project.Identifier)+"/wiki/"+url.PathEscape(req.PathValue("title"))+".json", url.Values{"include": {"attachments"}}, nil)
}

// attachment is what the relay needs of an attachment listed on an issue
// or a wiki page.
type attachment struct {
	ID          int    `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ContentURL  string `json:"content_url"`
}

func (r *relay) issueAttachment(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.Atoi(req.PathValue("id"))
	if err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "the issue id is a number")
		return
	}
	body, status, err := r.api.get("/issues/"+strconv.Itoa(id)+".json", url.Values{"include": {"attachments"}})
	if !r.fetched(w, status, err) {
		return
	}
	if err := r.issueProject(body); err != nil {
		gateway.WriteError(w, http.StatusForbidden, err.Error())
		return
	}
	var answer struct {
		Issue struct {
			Attachments []attachment `json:"attachments"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the Redmine answered something that is not an issue: "+err.Error())
		return
	}
	r.serveAttachment(w, req, answer.Issue.Attachments)
}

func (r *relay) wikiAttachment(w http.ResponseWriter, req *http.Request) {
	project, ok := r.pinned(w, req.PathValue("identifier"))
	if !ok {
		return
	}
	body, status, err := r.api.get("/projects/"+url.PathEscape(project.Identifier)+"/wiki/"+url.PathEscape(req.PathValue("title"))+".json", url.Values{"include": {"attachments"}})
	if !r.fetched(w, status, err) {
		return
	}
	var answer struct {
		WikiPage struct {
			Attachments []attachment `json:"attachments"`
		} `json:"wiki_page"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the Redmine answered something that is not a wiki page: "+err.Error())
		return
	}
	r.serveAttachment(w, req, answer.WikiPage.Attachments)
}

func (r *relay) fetched(w http.ResponseWriter, status int, err error) bool {
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the Redmine could not be reached: "+err.Error())
		return false
	}
	if status != http.StatusOK {
		gateway.WriteError(w, status, fmt.Sprintf("the Redmine answered %d", status))
		return false
	}
	return true
}

// serveAttachment downloads the attachment the issue or page lists and
// answers with its text, filtered: a text attachment as it is, a PDF or an
// image as the text extracted on the host. Nothing else passes, and the
// file itself never does. Errors name the attachment by id, since its file
// name has not been through the filter.
func (r *relay) serveAttachment(w http.ResponseWriter, req *http.Request, listed []attachment) {
	aid := req.PathValue("aid")
	var found *attachment
	for i := range listed {
		if strconv.Itoa(listed[i].ID) == aid {
			found = &listed[i]
		}
	}
	if found == nil {
		gateway.WriteError(w, http.StatusNotFound, fmt.Sprintf("attachment %s is not listed there", aid))
		return
	}
	isPDF := found.ContentType == "application/pdf"
	isText := strings.HasPrefix(found.ContentType, "text/") || found.ContentType == "application/json"
	if !isPDF && !isText && !imageTypes[found.ContentType] {
		gateway.WriteError(w, http.StatusUnsupportedMediaType, fmt.Sprintf("attachment %s is %s; only text, PDF and image attachments pass the filter", aid, found.ContentType))
		return
	}
	content, err := r.api.download(found.ContentURL, maxAttachment)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, fmt.Sprintf("download attachment %s: %s", aid, err))
		return
	}
	text := string(content)
	if !isText {
		if text, err = extractText(content, isPDF); err != nil {
			gateway.WriteError(w, http.StatusBadGateway, fmt.Sprintf("read the text of attachment %s: %s", aid, err))
			return
		}
	}
	filtered, marks := r.filter.Text(text)
	r.deliver(w, req, "text/plain; charset=utf-8", filtered, marks)
}
