A git-docs feed is a repository of documentation. Each entry is one file that was added or modified
since the last poll; its key is the path inside the repository and its revision is the file's blob id.

A unit for it covers one file: its source is the feed Source, its locator is the repository URL, a
`#` and the path (the entry's locator, already given), and it is read by the built-in `git` reader.
Name the pages the file feeds after its subject, not after its path, and keep one unit per file.
