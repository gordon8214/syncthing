# macOS extended-attribute synchronization

Folders that send or sync extended attributes default to Apple's syncable policy.
This applies to existing configurations too. Folders with attribute synchronization
disabled stay disabled; other operating systems retain their existing native policy.

In Syncthing's folder settings, choose **macOS extended attributes**:

- **Syncable attributes (recommended)** uses the operating system's
  `xattr_preserve_for_intent(name, XATTR_OPERATION_INTENT_SYNC)` API, including
  legacy defaults for Finder info, resource forks, tags, comments, text encoding,
  and quarantine. Checking a name only for a `#S` suffix is insufficient.
- **All attributes** includes unmarked application metadata as before.

The REST/XML field is `xattrFilter.macOSMode`, with values `syncable` and `all`.
An absent field means `syncable`. Unknown values are rejected.

Explicit filter rules override the selected default. Their existing first-match
and default-deny behavior is retained: once any rule exists, only permitted matches
are accepted. The size limits still apply in either mode; zero means unlimited.
Resource forks may contain file content and can exceed ordinary metadata sizes.

Scans and incoming writes use the same policy. Metadata-only updates leave excluded
local attributes untouched. File content replacement may discard excluded local
metadata. A rescan publishes the removal of excluded attributes from an old index;
subsequent excluded-attribute changes do not generate updates. Older peers' extra
attributes do not prevent convergence.

The Linux NAS continues to relay and archive foreign Mac metadata. Existing history
is retained unchanged, but excluded historical attributes are not applied on Macs
using the syncable policy. Attributes excluded at their source cannot be captured
in future NAS history. Select All attributes for archival completeness, or use
explicit filter rules for a custom set.

Upgrade participating Macs together before resuming affected folders. Back up their
configuration and database and snapshot NAS configuration, data, and history first.
Roll back binaries and configuration together if cutover fails. Changing back to
All attributes and rescanning republishes excluded attributes that still exist
locally; it cannot recover attributes already discarded during content replacement.
