// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package fswatcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cilium/hive/hivetest"
	"github.com/stretchr/testify/require"
)

func TestWatcher(t *testing.T) {
	logger := hivetest.Logger(t)
	tmp := t.TempDir()
	t.Log("using temporary directory", tmp)

	// regularFile := filepath.Join(tmp, "file")
	// regularSymlink := filepath.Join(tmp, "symlink")
	// nestedDir0 := filepath.Join(tmp, "foo")
	nestedDir0 := filepath.Join(tmp, "foo")
	nestedDir1 := filepath.Join(tmp, "foo", "bar")
	nestedFile := filepath.Join(nestedDir1, "nested")
	// directSymlink := filepath.Join(tmp, "foo", "symlink") // will point to nestedDir
	// indirectSymlink := filepath.Join(tmp, "foo", "symlink", "nested")
	// targetFile := filepath.Join(tmp, "target")

	w, err := New(logger, []string{
		// regularFile,
		//regularSymlink,
		nestedFile,
		//indirectSymlink,
	})
	require.NoError(t, err)
	defer w.Close()

	var lastName string
	assertEventName := func(name string) {
		t.Helper()

		for {
			select {
			case event := <-w.Events:
				// not every file operation deterministically emits the same
				// number of events, therefore report each name only once
				t.Log("event: ", event.Name, "op:", event.Op)
				if event.Name != lastName {
					require.Equal(t, name, event.Name)
					lastName = event.Name
					return
				}
			case err := <-w.Errors:
				t.Fatalf("unexpected error: %s", err)
			}
		}
	}

	// create $tmp/foo/ (this should not emit an event)
	/*t.Log("creating directory", tmp)
	fooDirectory := filepath.Join(tmp, "foo")
	err = os.MkdirAll(fooDirectory, 0777)
	require.NoError(t, err)

	// create $tmp/file
	var data = []byte("data")
	t.Log("creating regular file", regularFile)
	err = os.WriteFile(regularFile, data, 0777)
	require.NoError(t, err)
	assertEventName(regularFile)

	// symlink $tmp/symlink -> $tmp/target
	t.Log("creating symlink", regularSymlink)
	err = os.WriteFile(targetFile, data, 0777)
	require.NoError(t, err)
	err = os.Symlink(targetFile, regularSymlink)
	require.NoError(t, err)
	assertEventName(regularSymlink)
	*/
	var data = []byte("data")
	// create $tmp/foo/bar/nested
	t.Log("creating nested directory0", nestedDir0)
	err = os.MkdirAll(nestedDir0, 0777)
	require.NoError(t, err)
	time.Sleep(15 * time.Millisecond)
	t.Log("creating nested directory1", nestedDir1)
	err = os.MkdirAll(nestedDir1, 0777)
	require.NoError(t, err)
	//time.Sleep(1500 * time.Millisecond)
	t.Log("creating nested file", nestedFile)
	err = os.WriteFile(nestedFile, data, 0777)
	require.NoError(t, err)
	assertEventName(nestedFile)

	/*
		// symlink $tmp/foo/symlink -> $tmp/foo/bar (this will emit an event on indirectSymlink)
		t.Log("creating direct symlink", directSymlink)
		err = os.Symlink(nestedDir, directSymlink)
		require.NoError(t, err)
		assertEventName(indirectSymlink)

		// redirect $tmp/symlink -> $tmp/file (this will not emit an event)
		t.Log("rewriting symlink", regularSymlink)
		err = os.Remove(regularSymlink)
		require.NoError(t, err)
		err = os.Symlink(regularFile, regularSymlink)
		require.NoError(t, err)
		select {
		case n := <-w.Events:
			t.Fatalf("rewriting symlink emitted unexpected event on %q", n)
		default:
		}

		// delete $tmp/target (this will emit an event on regularSymlink)
		t.Log("deleting target file", targetFile)
		err = os.Remove(targetFile)
		require.NoError(t, err)
		assertEventName(regularSymlink)
	*/
}

func TestHasParent(t *testing.T) {
	type args struct {
		path   string
		parent string
	}
	tests := []struct {
		args args
		want bool
	}{
		{args: args{"/foo/bar", "/foo"}, want: true},

		{args: args{"/foo", "/foo/"}, want: true},
		{args: args{"/foo/", "/foo"}, want: true},
		{args: args{"/foo", "/foo/bar"}, want: false},
		{args: args{"/foo", "/foo/bar/baz"}, want: false},

		{args: args{"/foo/bar/baz/", "/foo"}, want: true},
		{args: args{"/foo/bar/baz/", "/foo/bar"}, want: true},
		{args: args{"/foo/bar/baz/", "/foo/baz"}, want: false},

		{args: args{"/foobar/baz", "/foo"}, want: false},

		{args: args{"/foo/..", "/foo"}, want: false},
		{args: args{"/foo/.", "/foo/.."}, want: true},
		{args: args{"/foo/.", "/foo"}, want: true},
		{args: args{"/foo/.", "/"}, want: true},
	}
	for _, tt := range tests {
		got := hasParent(tt.args.path, tt.args.parent)
		if got != tt.want {
			t.Fatalf("unexpected result %t for hasParent(%q, %q)", got, tt.args.path, tt.args.parent)
		}
	}
}
