# Cleanup recovery

Confirmed cleanup validates all native paths and Git operands before effects,
then streams existing source and destination operands into a private archive.
Archive entries retain slash-separated names (C1-10 required no code change).
A complete archive is flushed and closed before exclusive publication. An archive
publication collision never overwrites recovery data. Unpublished staging files
are removed on failure; published archives remain if subsequent durability fails.
Cleanup stops before actions when backup creation or durability fails.

Apply results retain the backup identity, count, action outcomes, and attempted
manifest identity when manifest persistence fails. Action, cancellation, and
manifest errors are joined. A reported manifest path is an attempted identity;
check its existence before using it as durable recovery evidence. Restoration is
always manual: inspect the archive and recorded action paths, recover selected
files into a separate directory, then reconcile the working tree and Git index.
Cleanup never automatically restores files or replays failed actions.

Unix publication uses an exclusive hard link, then directory fsync. Only EINVAL
and ENOTSUP from directory fsync are treated as unsupported filesystem operations.
Windows uses MoveFileEx with WRITE_THROUGH after file flush and close; Go directory
handles do not support directory fsync. Native Windows tests must establish archive
contents, path containment, and collision behavior; cross-compilation is insufficient.
