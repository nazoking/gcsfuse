// Copyright 2015 Google Inc. All Rights Reserved.
// Author: jacobsa@google.com (Aaron Jacobs)
//
// Tests registered by RegisterFSTests.

package fstesting

import (
	"io/ioutil"
	"log"
	"math"
	"os"
	"path"
	"syscall"
	"time"

	"github.com/jacobsa/gcloud/gcs"
	"github.com/jacobsa/gcloud/gcs/gcsutil"
	"github.com/jacobsa/gcsfuse/fs"
	"github.com/jacobsa/gcsfuse/fuseutil"
	"github.com/jacobsa/gcsfuse/timeutil"
	. "github.com/jacobsa/oglematchers"
	. "github.com/jacobsa/ogletest"
	"golang.org/x/net/context"
	"google.golang.org/cloud/storage"
)

////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////

func getFileNames(entries []os.FileInfo) (names []string) {
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return
}

////////////////////////////////////////////////////////////////////////
// Common
////////////////////////////////////////////////////////////////////////

type fsTest struct {
	ctx    context.Context
	clock  timeutil.SimulatedClock
	bucket gcs.Bucket
	mfs    *fuseutil.MountedFileSystem
}

var _ fsTestInterface = &fsTest{}

func (t *fsTest) setUpFsTest(b gcs.Bucket) {
	t.ctx = context.Background()
	t.bucket = b

	// Set up a temporary directory for mounting.
	mountPoint, err := ioutil.TempDir("", "fs_test")
	if err != nil {
		panic("ioutil.TempDir: " + err.Error())
	}

	// Mount a file system.
	fileSystem, err := fs.NewFuseFS(&t.clock, b)
	if err != nil {
		panic("NewFuseFS: " + err.Error())
	}

	t.mfs = fuseutil.MountFileSystem(mountPoint, fileSystem)
	if err := t.mfs.WaitForReady(t.ctx); err != nil {
		panic("MountedFileSystem.WaitForReady: " + err.Error())
	}
}

func (t *fsTest) tearDownFsTest() {
	// Unmount the file system.
	if err := t.mfs.Unmount(); err != nil {
		panic("MountedFileSystem.Unmount: " + err.Error())
	}

	if err := t.mfs.Join(t.ctx); err != nil {
		panic("MountedFileSystem.Join: " + err.Error())
	}
}

func (t *fsTest) createObjects(objects []*gcsutil.ObjectInfo) error {
	_, err := gcsutil.CreateObjects(t.ctx, t.bucket, objects)
	return err
}

func (t *fsTest) createEmptyObjects(names []string) error {
	_, err := gcsutil.CreateEmptyObjects(t.ctx, t.bucket, names)
	return err
}

////////////////////////////////////////////////////////////////////////
// Read-only interaction
////////////////////////////////////////////////////////////////////////

type readOnlyTest struct {
	fsTest
}

// Repeatedly call ioutil.ReadDir until an error is encountered or until the
// result has the given length. After each successful call with the wrong
// length, advance the clock by more than the directory listing cache TTL in
// order to flush the cache before the next call.
//
// This is a hacky workaround for the lack of list-after-write consistency in
// GCS that must be used when interacting with GCS through a side channel
// rather than through the file system. We set up some objects through a back
// door, then list repeatedly until we see the state we hope to see.
func (t *readOnlyTest) readDirUntil(
	desiredLen int,
	dir string) (entries []os.FileInfo, err error) {
	startTime := time.Now()
	for i := 1; ; i++ {
		entries, err = ioutil.ReadDir(dir)
		if err != nil || len(entries) == desiredLen {
			return
		}

		t.clock.AdvanceTime(2 * fs.DirListingCacheTTL)

		// If this is taking a long time, log that fact so that the user can tell
		// why the test is hanging.
		if time.Since(startTime) > 5*time.Second {
			log.Printf("readDirUntil waiting for length %v...", desiredLen)
		}
	}
}

func (t *readOnlyTest) EmptyRoot() {
	// ReadDir
	entries, err := t.readDirUntil(0, t.mfs.Dir())
	AssertEq(nil, err)

	ExpectThat(entries, ElementsAre())
}

func (t *readOnlyTest) ContentsInRoot() {
	// Set up contents.
	AssertEq(
		nil,
		t.createObjects(
			[]*gcsutil.ObjectInfo{
				// File
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "foo",
					},
					Contents: "taco",
				},

				// Directory
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "bar/",
					},
				},

				// File
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "baz",
					},
					Contents: "burrito",
				},

				// File in sub-directory
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "qux/asdf",
					},
					Contents: "",
				},
			}))

	// ReadDir
	entries, err := t.readDirUntil(4, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(4, len(entries), "Names: %v", getFileNames(entries))
	var e os.FileInfo

	// bar
	e = entries[0]
	ExpectEq("bar", e.Name())
	ExpectEq(0, e.Size())
	ExpectEq(os.ModeDir|os.FileMode(0500), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectTrue(e.IsDir())

	// baz
	e = entries[1]
	ExpectEq("baz", e.Name())
	ExpectEq(len("burrito"), e.Size())
	ExpectEq(os.FileMode(0400), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectFalse(e.IsDir())

	// foo
	e = entries[2]
	ExpectEq("foo", e.Name())
	ExpectEq(len("taco"), e.Size())
	ExpectEq(os.FileMode(0400), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectFalse(e.IsDir())

	// qux
	e = entries[3]
	ExpectEq("qux", e.Name())
	ExpectEq(0, e.Size())
	ExpectEq(os.ModeDir|os.FileMode(0500), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectTrue(e.IsDir())
}

func (t *readOnlyTest) EmptySubDirectory() {
	// Set up an empty directory placeholder called 'bar'.
	AssertEq(nil, t.createEmptyObjects([]string{"bar/"}))

	// ReadDir
	_, err := t.readDirUntil(1, t.mfs.Dir())
	AssertEq(nil, err)

	entries, err := t.readDirUntil(0, path.Join(t.mfs.Dir(), "bar"))
	AssertEq(nil, err)

	ExpectThat(entries, ElementsAre())
}

func (t *readOnlyTest) ContentsInSubDirectory_PlaceholderPresent() {
	// Set up contents.
	AssertEq(
		nil,
		t.createObjects(
			[]*gcsutil.ObjectInfo{
				// Placeholder
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/",
					},
					Contents: "",
				},

				// File
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/foo",
					},
					Contents: "taco",
				},

				// Directory
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/bar/",
					},
				},

				// File
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/baz",
					},
					Contents: "burrito",
				},

				// File in sub-directory
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/qux/asdf",
					},
					Contents: "",
				},
			}))

	// Wait for the directory to show up in the file system.
	_, err := t.readDirUntil(1, path.Join(t.mfs.Dir()))
	AssertEq(nil, err)

	// ReadDir
	entries, err := t.readDirUntil(4, path.Join(t.mfs.Dir(), "dir"))
	AssertEq(nil, err)

	AssertEq(4, len(entries), "Names: %v", getFileNames(entries))
	var e os.FileInfo

	// bar
	e = entries[0]
	ExpectEq("bar", e.Name())
	ExpectEq(0, e.Size())
	ExpectEq(os.ModeDir|os.FileMode(0500), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectTrue(e.IsDir())

	// baz
	e = entries[1]
	ExpectEq("baz", e.Name())
	ExpectEq(len("burrito"), e.Size())
	ExpectEq(os.FileMode(0400), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectFalse(e.IsDir())

	// foo
	e = entries[2]
	ExpectEq("foo", e.Name())
	ExpectEq(len("taco"), e.Size())
	ExpectEq(os.FileMode(0400), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectFalse(e.IsDir())

	// qux
	e = entries[3]
	ExpectEq("qux", e.Name())
	ExpectEq(0, e.Size())
	ExpectEq(os.ModeDir|os.FileMode(0500), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectTrue(e.IsDir())
}

func (t *readOnlyTest) ContentsInSubDirectory_PlaceholderNotPresent() {
	// Set up contents.
	AssertEq(
		nil,
		t.createObjects(
			[]*gcsutil.ObjectInfo{
				// File
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/foo",
					},
					Contents: "taco",
				},

				// Directory
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/bar/",
					},
				},

				// File
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/baz",
					},
					Contents: "burrito",
				},

				// File in sub-directory
				&gcsutil.ObjectInfo{
					Attrs: storage.ObjectAttrs{
						Name: "dir/qux/asdf",
					},
					Contents: "",
				},
			}))

	// Wait for the directory to show up in the file system.
	_, err := t.readDirUntil(1, path.Join(t.mfs.Dir()))
	AssertEq(nil, err)

	// ReadDir
	entries, err := t.readDirUntil(4, path.Join(t.mfs.Dir(), "dir"))
	AssertEq(nil, err)

	AssertEq(4, len(entries), "Names: %v", getFileNames(entries))
	var e os.FileInfo

	// bar
	e = entries[0]
	ExpectEq("bar", e.Name())
	ExpectEq(0, e.Size())
	ExpectEq(os.ModeDir|os.FileMode(0500), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectTrue(e.IsDir())

	// baz
	e = entries[1]
	ExpectEq("baz", e.Name())
	ExpectEq(len("burrito"), e.Size())
	ExpectEq(os.FileMode(0400), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectFalse(e.IsDir())

	// foo
	e = entries[2]
	ExpectEq("foo", e.Name())
	ExpectEq(len("taco"), e.Size())
	ExpectEq(os.FileMode(0400), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectFalse(e.IsDir())

	// qux
	e = entries[3]
	ExpectEq("qux", e.Name())
	ExpectEq(0, e.Size())
	ExpectEq(os.ModeDir|os.FileMode(0500), e.Mode())
	ExpectLt(math.Abs(time.Since(e.ModTime()).Seconds()), 30)
	ExpectTrue(e.IsDir())
}

func (t *readOnlyTest) ListDirectoryTwice_NoChange() {
	// Set up initial contents.
	AssertEq(
		nil,
		t.createEmptyObjects([]string{
			"foo",
			"bar",
		}))

	// List once.
	entries, err := t.readDirUntil(2, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(2, len(entries), "Names: %v", getFileNames(entries))
	ExpectEq("bar", entries[0].Name())
	ExpectEq("foo", entries[1].Name())

	// List again.
	entries, err = t.readDirUntil(2, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(2, len(entries), "Names: %v", getFileNames(entries))
	ExpectEq("bar", entries[0].Name())
	ExpectEq("foo", entries[1].Name())
}

func (t *readOnlyTest) ListDirectoryTwice_Changed_CacheStillValid() {
	// Set up initial contents.
	AssertEq(
		nil,
		t.createEmptyObjects([]string{
			"foo",
			"bar",
		}))

	// List once.
	entries, err := t.readDirUntil(2, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(2, len(entries), "Names: %v", getFileNames(entries))
	ExpectEq("bar", entries[0].Name())
	ExpectEq("foo", entries[1].Name())

	// Add "baz" and remove "bar".
	AssertEq(nil, t.bucket.DeleteObject(t.ctx, "bar"))
	AssertEq(nil, t.createEmptyObjects([]string{"baz"}))

	// Advance the clock to just before the cache expiry.
	t.clock.AdvanceTime(fs.DirListingCacheTTL - time.Millisecond)

	// List again.
	entries, err = t.readDirUntil(2, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(2, len(entries), "Names: %v", getFileNames(entries))
	ExpectEq("bar", entries[0].Name())
	ExpectEq("foo", entries[1].Name())
}

func (t *readOnlyTest) ListDirectoryTwice_Changed_CacheInvalidated() {
	// Set up initial contents.
	AssertEq(
		nil,
		t.createEmptyObjects([]string{
			"foo",
			"bar",
		}))

	// List once.
	entries, err := t.readDirUntil(2, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(2, len(entries), "Names: %v", getFileNames(entries))
	ExpectEq("bar", entries[0].Name())
	ExpectEq("foo", entries[1].Name())

	// Add "baz" and remove "bar".
	AssertEq(nil, t.bucket.DeleteObject(t.ctx, "bar"))
	AssertEq(nil, t.createEmptyObjects([]string{"baz"}))

	// Advance the clock to just after the cache expiry.
	t.clock.AdvanceTime(fs.DirListingCacheTTL + time.Millisecond)

	// List again.
	entries, err = t.readDirUntil(2, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(2, len(entries), "Names: %v", getFileNames(entries))
	ExpectEq("baz", entries[0].Name())
	ExpectEq("foo", entries[1].Name())
}

func (t *readOnlyTest) Inodes() {
	// Set up two files and a directory placeholder.
	AssertEq(
		nil,
		t.createEmptyObjects([]string{
			"foo",
			"bar/",
			"baz",
		}))

	// List.
	entries, err := t.readDirUntil(3, t.mfs.Dir())
	AssertEq(nil, err)

	AssertEq(3, len(entries), "Names: %v", getFileNames(entries))

	// Confirm all of the inodes are distinct.
	inodesSeen := make(map[uint64]struct{})
	for _, fileInfo := range entries {
		stat := fileInfo.Sys().(*syscall.Stat_t)
		_, ok := inodesSeen[stat.Ino]
		AssertFalse(ok, "Duplicate inode: %v", fileInfo)

		inodesSeen[stat.Ino] = struct{}{}
	}
}

func (t *readOnlyTest) OpenNonExistentFile() {
	_, err := os.Open(path.Join(t.mfs.Dir(), "foo"))

	AssertNe(nil, err)
	ExpectThat(err, Error(HasSubstr("foo")))
	ExpectThat(err, Error(HasSubstr("no such file")))
}
