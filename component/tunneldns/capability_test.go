package tunneldns

import (
	"testing"
	"time"
)

func TestRegistryRemembersOneVerdictPerNode(t *testing.T) {
	now := time.Unix(1000, 0)
	r := NewRegistry()
	r.now = func() time.Time { return now }

	// A node nobody has tried is worth asking.
	if !r.Supported("JP-01") {
		t.Fatal("an unknown node must be tried")
	}

	r.MarkUnsupported("JP-02")
	if r.Supported("JP-02") {
		t.Fatal("a node that could not answer must not be asked again")
	}
	if !r.Supported("JP-01") {
		t.Fatal("one node's verdict must not apply to another")
	}

	// The verdict expires, so an upgraded server is found again.
	now = now.Add(UnsupportedTTL)
	if !r.Supported("JP-02") {
		t.Fatal("an expired verdict must be forgotten")
	}

	r.MarkUnsupported("JP-02")
	r.MarkSupported("JP-02")
	if !r.Supported("JP-02") {
		t.Fatal("a node that answered must be asked again")
	}
}

func TestRegistryResetForgetsEverything(t *testing.T) {
	r := NewRegistry()
	r.MarkUnsupported("JP-01")
	r.Reset()
	if !r.Supported("JP-01") {
		t.Fatal("a reload replaces the proxy set; old verdicts must not survive it")
	}
}

// An unnamed node cannot be told apart from any other, so nothing is recorded
// against it rather than one verdict standing in for all of them.
func TestRegistryIgnoresAnEmptyNode(t *testing.T) {
	r := NewRegistry()
	r.MarkUnsupported("")
	if !r.Supported("") {
		t.Fatal("an unnamed node must not be silenced")
	}
}

func TestRegistryIsConcurrent(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 200 {
				r.Supported("JP-01")
				r.MarkUnsupported("JP-01")
				r.MarkSupported("JP-01")
			}
		}()
	}
	for range 8 {
		<-done
	}
}
