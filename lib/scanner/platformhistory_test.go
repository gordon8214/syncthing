package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

func TestScanRestoredForeignPlatformData(t *testing.T) {
	for _, pd := range []protocol.PlatformData{
		{}, {Darwin: &protocol.XattrData{Xattrs: []protocol.Xattr{{Name: "com.apple.ResourceFork", Value: []byte("historical fork")}}}},
	} {
		cfg, cancel := testConfig()
		cfg.Filesystem = fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
		path := filepath.Join(cfg.Filesystem.URI(), "file")
		if err := os.WriteFile(path, []byte("unchanged contents"), 0o600); err != nil {
			t.Fatal(err)
		}
		var current protocol.FileInfo
		for res := range Walk(context.Background(), cfg) {
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			current = res.File
		}
		current.Platform = protocol.PlatformData{Darwin: &protocol.XattrData{Xattrs: []protocol.Xattr{{Name: "tag", Value: []byte("latest")}}}}
		cfg.CurrentFiler = fakeCurrentFiler{"file": current}
		cfg.ScanXattrs = false
		cfg.RestoredPlatformData = func(string) (protocol.PlatformData, bool, error) { return pd, true, nil }
		beforeVersion := current.Version.Copy()
		count := 0
		for res := range Walk(context.Background(), cfg) {
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			count++
			if !reflect.DeepEqual(res.File.Platform, pd) {
				t.Fatal("restored metadata merged with latest")
			}
			if res.File.Version.Equal(beforeVersion) {
				t.Fatal("restore did not advance version")
			}
		}
		if count != 1 {
			t.Fatalf("metadata-only restore yielded %d files", count)
		}
		cancel()
	}
}
