package child

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

const maxRequestBody = 1 << 20

// Event describes a child the relay created, started, stopped or deleted
// for the VM, or the request it refused, so the caller can record it in the
// VM audit log.
type Event struct {
	Operation  string
	Name       string
	Instance   string
	Shape      string
	Expose     string
	ExternalIP string
	Result     string
}

// Cloud is what the relay needs from GCP; vmcfg provides it, tests fake it.
type Cloud interface {
	Create(spec vmcfg.ChildSpec, debug bool) (vmcfg.ChildInstance, error)
	List(parent vmcfg.GCPConnection) ([]vmcfg.ChildInstance, error)
	Delete(parent vmcfg.GCPConnection, instance string, debug bool) error
	Start(parent vmcfg.GCPConnection, instance string, debug bool) error
	Stop(parent vmcfg.GCPConnection, instance string, debug bool) error
}

type gcpCloud struct{}

func (gcpCloud) Create(spec vmcfg.ChildSpec, debug bool) (vmcfg.ChildInstance, error) {
	return vmcfg.CreateChildInstance(spec, debug)
}
func (gcpCloud) List(parent vmcfg.GCPConnection) ([]vmcfg.ChildInstance, error) {
	return vmcfg.ListChildInstances(parent)
}
func (gcpCloud) Delete(parent vmcfg.GCPConnection, instance string, debug bool) error {
	return vmcfg.DeleteChildInstance(parent, instance, debug)
}
func (gcpCloud) Start(parent vmcfg.GCPConnection, instance string, debug bool) error {
	return vmcfg.StartChildInstance(parent, instance, debug)
}
func (gcpCloud) Stop(parent vmcfg.GCPConnection, instance string, debug bool) error {
	return vmcfg.StopChildInstance(parent, instance, debug)
}

// GCP is the Cloud backed by the host's gcloud.
var GCP Cloud = gcpCloud{}

// Relay creates, starts, stops and deletes children of one parent within
// the agreement.
type Relay struct {
	cfg         ProjectConfig
	cloud       Cloud
	parent      vmcfg.GCPConnection
	projectHash string
	defaults    Defaults
	// mu keeps the count the agreement bounds from being checked by two
	// creates at once.
	mu sync.Mutex
}

// Defaults fill what a request leaves out: the project's VM image and a
// disk the size of the parent's.
type Defaults struct {
	BootDiskSize string
	ImageFamily  string
	ImageProject string
}

func NewRelay(cfg ProjectConfig, cloud Cloud, parent vmcfg.GCPConnection, projectHash string, defaults Defaults) *Relay {
	return &Relay{cfg: cfg, cloud: cloud, parent: parent, projectHash: projectHash, defaults: defaults}
}

// Handler serves the VM; the host gateway mounts it under /child.
func Handler(relay *Relay, onEvent func(Event) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"agreement": relay.cfg, "defaults": relay.defaults, "parent": relay.parent.InstanceName})
	})
	mux.HandleFunc("GET /list", func(w http.ResponseWriter, _ *http.Request) {
		children, err := relay.cloud.List(relay.parent)
		if err != nil {
			gateway.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		if children == nil {
			children = []vmcfg.ChildInstance{}
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"children": children})
	})
	mux.HandleFunc("POST /create", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if !decodeBody(w, r, &req) {
			return
		}
		result, event, err := relay.create(req)
		if recordErr := onEvent(event); recordErr != nil {
			err = errors.Join(err, fmt.Errorf("recording the audit event failed: %w", recordErr))
		}
		if err != nil {
			writeError(w, err)
			return
		}
		gateway.WriteJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /delete", func(w http.ResponseWriter, r *http.Request) {
		name, ok := decodeName(w, r)
		if !ok {
			return
		}
		event, err := relay.delete(name)
		if recordErr := onEvent(event); recordErr != nil {
			err = errors.Join(err, fmt.Errorf("recording the audit event failed: %w", recordErr))
		}
		if err != nil {
			writeError(w, err)
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]string{"instance": event.Instance})
	})
	for _, operation := range []string{"start", "stop"} {
		mux.HandleFunc("POST /"+operation, func(w http.ResponseWriter, r *http.Request) {
			name, ok := decodeName(w, r)
			if !ok {
				return
			}
			child, event, err := relay.power(operation, name)
			if recordErr := onEvent(event); recordErr != nil {
				err = errors.Join(err, fmt.Errorf("recording the audit event failed: %w", recordErr))
			}
			if err != nil {
				writeError(w, err)
				return
			}
			gateway.WriteJSON(w, http.StatusOK, map[string]any{"child": child})
		})
	}
	return mux
}

// RefusedError reports a request outside the agreement; the credential is
// never used for it.
type RefusedError struct{ err error }

func (e *RefusedError) Error() string { return e.err.Error() }

var errNoChild = errors.New("no such child VM")

// create fills the defaults, checks the agreement, makes a key pair and
// the instance, and answers with the private key: the parent is the only
// place it will ever be.
func (r *Relay) create(req Request) (map[string]any, Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.BootDiskSize == "" {
		req.BootDiskSize = r.defaults.BootDiskSize
	}
	if req.ImageFamily == "" && req.ImageProject == "" {
		req.ImageFamily, req.ImageProject = r.defaults.ImageFamily, r.defaults.ImageProject
	}
	event := Event{Operation: "create", Name: req.Name, Shape: fmt.Sprintf("%s %s %s/%s", req.MachineType, req.BootDiskSize, req.ImageProject, req.ImageFamily), Expose: portList(req.Expose)}
	existing, err := r.cloud.List(r.parent)
	if err != nil {
		event.Result = err.Error()
		return nil, event, err
	}
	for _, child := range existing {
		if child.Name == req.Name {
			event.Result = "refused"
			return nil, event, &RefusedError{fmt.Errorf("a child VM named %s exists already", req.Name)}
		}
	}
	if err := r.cfg.Allows(req, len(existing)); err != nil {
		event.Result = "refused"
		return nil, event, &RefusedError{err}
	}
	publicKey, privateKey, err := newKeyPair(req.Name)
	if err != nil {
		event.Result = err.Error()
		return nil, event, err
	}
	child, err := r.cloud.Create(vmcfg.ChildSpec{
		Parent: r.parent, ProjectHash: r.projectHash, Name: req.Name, User: req.User, PublicKey: publicKey,
		MachineType: req.MachineType, BootDiskSize: req.BootDiskSize, ImageFamily: req.ImageFamily, ImageProject: req.ImageProject,
		Expose: req.Expose,
	}, false)
	event.Instance = vmcfg.ChildInstanceName(r.parent.InstanceName, req.Name)
	if err != nil {
		event.Result = err.Error()
		return nil, event, err
	}
	event.ExternalIP = child.ExternalIP
	event.Result = child.Status
	return map[string]any{"child": child, "user": req.User, "private_key": privateKey}, event, nil
}

func (r *Relay) delete(name string) (Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event := Event{Operation: "delete", Name: name}
	children, err := r.cloud.List(r.parent)
	if err != nil {
		event.Result = err.Error()
		return event, err
	}
	for _, child := range children {
		if child.Name != name {
			continue
		}
		event.Instance = child.Instance
		if err := r.cloud.Delete(r.parent, child.Instance, false); err != nil {
			event.Result = err.Error()
			return event, err
		}
		event.Result = "deleted"
		return event, nil
	}
	event.Result = errNoChild.Error()
	return event, fmt.Errorf("%s: %w", name, errNoChild)
}

// power starts or stops a child that exists and answers with the child as
// it is afterwards: a start gives it a new external IP.
func (r *Relay) power(operation, name string) (vmcfg.ChildInstance, Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event := Event{Operation: operation, Name: name}
	child, err := r.find(name)
	if err != nil {
		event.Result = err.Error()
		return vmcfg.ChildInstance{}, event, err
	}
	event.Instance = child.Instance
	if operation == "start" {
		err = r.cloud.Start(r.parent, child.Instance, false)
	} else {
		err = r.cloud.Stop(r.parent, child.Instance, false)
	}
	if err != nil {
		event.Result = err.Error()
		return vmcfg.ChildInstance{}, event, err
	}
	if child, err = r.find(name); err != nil {
		event.Result = err.Error()
		return vmcfg.ChildInstance{}, event, err
	}
	event.ExternalIP = child.ExternalIP
	event.Result = child.Status
	return child, event, nil
}

func (r *Relay) find(name string) (vmcfg.ChildInstance, error) {
	children, err := r.cloud.List(r.parent)
	if err != nil {
		return vmcfg.ChildInstance{}, err
	}
	for _, child := range children {
		if child.Name == name {
			return child, nil
		}
	}
	return vmcfg.ChildInstance{}, fmt.Errorf("%s: %w", name, errNoChild)
}

// newKeyPair returns an authorized_keys line and the matching OpenSSH
// private key.
func newKeyPair(comment string) (string, string, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(private, "isotty-child "+comment)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))), string(pem.EncodeToMemory(block)), nil
}

func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return "", false
	}
	return req.Name, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, err error) {
	var refused *RefusedError
	switch {
	case errors.As(err, &refused):
		gateway.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errNoChild):
		gateway.WriteError(w, http.StatusNotFound, err.Error())
	default:
		gateway.WriteError(w, http.StatusBadGateway, err.Error())
	}
}
