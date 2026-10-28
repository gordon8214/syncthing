package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/versioner"
)

func TestHistoricalPlatformRestoreThroughModel(t *testing.T) {
	w, cfg := newDefaultCfgWrapper(t)
	cfg.FilesystemType = config.FilesystemTypeBasic
	cfg.Path = t.TempDir()
	cfg.Versioning = config.VersioningConfiguration{Type: "staggered", Params: map[string]string{"preservePlatformData": "true"}}
	cfg.IgnorePerms = true
	must(t, os.Mkdir(filepath.Join(cfg.Path, ".stfolder"), 0o700))
	_, err := w.Modify(func(c *config.Configuration) { c.SetFolder(cfg) })
	must(t, err)
	m := setupModel(t, w)
	defer cleanupModel(m)
	path := filepath.Join(cfg.Path, "file.txt")
	must(t, os.WriteFile(path, []byte("old contents"), 0o644))
	must(t, m.ScanFolder(cfg.ID))
	oldPD := protocol.PlatformData{Darwin: &protocol.XattrData{Xattrs: []protocol.Xattr{{Name: "com.apple.ResourceFork", Value: []byte("old fork")}}}}
	fi, _, err := m.sdb.GetDeviceFile(cfg.ID, protocol.LocalDeviceID, "file.txt")
	must(t, err)
	fi.Platform = oldPD
	must(t, m.sdb.Update(cfg.ID, protocol.LocalDeviceID, []protocol.FileInfo{fi}))
	v := m.folderVersioners[cfg.ID]
	pv, ok := versioner.WithPlatformData(v)
	if !ok {
		t.Fatal("no metadata versioner")
	}
	must(t, pv.ArchiveWithPlatformData("file.txt", oldPD, false))
	versions, err := v.GetVersions()
	must(t, err)
	must(t, os.WriteFile(path, []byte("new contents"), 0o644))
	must(t, m.ScanFolder(cfg.ID))
	fi, _, err = m.sdb.GetDeviceFile(cfg.ID, protocol.LocalDeviceID, "file.txt")
	must(t, err)
	fi.Platform = protocol.PlatformData{Darwin: &protocol.XattrData{Xattrs: []protocol.Xattr{{Name: "com.apple.ResourceFork", Value: []byte("new fork")}}}}
	must(t, m.sdb.Update(cfg.ID, protocol.LocalDeviceID, []protocol.FileInfo{fi}))
	result, err := m.RestoreFolderVersions(cfg.ID, map[string]time.Time{"file.txt": versions["file.txt"][0].VersionTime})
	must(t, err)
	if len(result) != 0 {
		t.Fatal(result)
	}
	fi, _, err = m.sdb.GetDeviceFile(cfg.ID, protocol.LocalDeviceID, "file.txt")
	must(t, err)
	if !reflect.DeepEqual(fi.Platform, oldPD) {
		t.Fatalf("historical foreign metadata not indexed: %+v", fi.Platform)
	}
	body, err := os.ReadFile(path)
	must(t, err)
	if string(body) != "old contents" {
		t.Fatalf("restored %q", body)
	}
	if _, pending, err := pv.RestoredPlatformData("file.txt"); err != nil || pending {
		t.Fatalf("not acknowledged: %v %v", pending, err)
	}
	// An ignored restored file cannot be acknowledged. REST must report that
	// per-path failure rather than claiming a complete metadata restoration.
	versions, err = v.GetVersions()
	must(t, err)
	if len(versions["file.txt"]) == 0 {
		t.Fatal("current version not archived during restore")
	}
	must(t, m.SetIgnores(cfg.ID, []string{"file.txt"}))
	result, err = m.RestoreFolderVersions(cfg.ID, map[string]time.Time{"file.txt": versions["file.txt"][0].VersionTime})
	must(t, err)
	if result["file.txt"] == nil {
		t.Fatal("ignored restore reported complete")
	}

}
