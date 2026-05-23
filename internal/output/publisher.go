package output

import (
	"io"
	"slices"
	"strings"
	"sync"
	"time"
)

type Result struct {
	Name            string
	URL             string
	Method          string
	Up              bool
	Status          int
	LatencyMS       int64
	SSLDaysLeft     int
	SSLIssuer       string
	SSLSubject      string
	SSLDNSNames     string
	FailedAssertion string
	Error           string
	Body            string
	Headers         map[string]string
	Timestamp       time.Time
	Tags            map[string]string
}

type RuntimeContext struct {
	store map[string]any
}

func NewRuntimeContext() RuntimeContext { return RuntimeContext{store: make(map[string]any)} }

func (c RuntimeContext) GetBool(key string) bool {
	if val, ok := c.store[key].(*bool); ok && val != nil {
		return *val
	}
	return false
}

func (c RuntimeContext) GetString(key string) string {
	if val, ok := c.store[key].(*string); ok && val != nil {
		return *val
	}
	return ""
}

func (c RuntimeContext) GetStrings(key string) []string {
	if val, ok := c.store[key].(*[]string); ok && val != nil {
		return *val
	}

	return nil
}

func (c RuntimeContext) GetWriter(key string) io.Writer {
	if val, ok := c.store[key].(io.Writer); ok {
		return val
	}

	return nil
}

func (c RuntimeContext) OutputSelected(name Name) bool {
	return slices.ContainsFunc(c.GetStrings(string(SelectionKey)), func(value string) bool {
		return Name(strings.TrimSpace(value)) == name
	})
}

func (c RuntimeContext) Set(key string, ptr any) { c.store[key] = ptr }

type ExtensionHook struct {
	SetupFlags func(ctx RuntimeContext, registerFlag func(func()))
	Factory    func(ctx RuntimeContext) (Publisher, error)
}

type Publisher interface {
	Publish(res Result) error
	Close() error
}

var Registry = make(map[string]ExtensionHook)
var registryMu sync.RWMutex

func Register(name string, hook ExtensionHook) {
	registryMu.Lock()
	defer registryMu.Unlock()
	Registry[name] = hook
}

func Get(name Name) (ExtensionHook, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	hook, ok := Registry[string(name)]
	return hook, ok
}
