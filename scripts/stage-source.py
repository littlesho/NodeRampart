#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Copy only the reviewed per-file source manifest, without requiring Git."""

import os
from pathlib import PurePosixPath
import re
import shutil
import stat
import sys


def open_source(root, relative):
    parts = relative.split("/")
    current = os.dup(root)
    try:
        for component in parts[:-1]:
            child = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=current)
            os.close(current)
            current = child
        fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=current)
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            os.close(fd)
            raise ValueError("source manifest requires regular files")
        return fd
    finally:
        os.close(current)


def main():
    if len(sys.argv) != 3:
        raise ValueError("usage: stage-source.py SOURCE_DIRECTORY EMPTY_DESTINATION")
    root = os.open(sys.argv[1], os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    destination = os.open(sys.argv[2], os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        if os.listdir(destination):
            raise ValueError("source staging destination must be empty")
        with os.fdopen(open_source(root, "packaging/source-files.txt"), "rb") as manifest:
            raw = manifest.read((1 << 20) + 1)
        if len(raw) > 1 << 20:
            raise ValueError("source manifest exceeds its size limit")
        names = []
        seen = set()
        for line in raw.decode("ascii").splitlines():
            if not line or line.startswith("#"):
                continue
            parts = line.split("/")
            if (not re.fullmatch(r"[A-Za-z0-9_.+/-]+", line) or
                    any(part in ("", ".", "..") for part in parts) or
                    str(PurePosixPath(line)) != line):
                raise ValueError("unsafe source manifest path")
            if (line in seen or any(part in (".git", "AGENTS.md", "STATUS.md", ".env") or
                                   part.startswith(".env.") for part in parts) or
                    line.startswith("docs/ai/")):
                raise ValueError("duplicate or private source manifest path")
            names.append(line)
            seen.add(line)
        if not names or len(names) > 4096:
            raise ValueError("source manifest has an invalid file count")
        for name in names:
            with os.fdopen(open_source(root, name), "rb") as source:
                parent = os.dup(destination)
                try:
                    parts = name.split("/")
                    for part in parts[:-1]:
                        try:
                            os.mkdir(part, 0o755, dir_fd=parent)
                        except FileExistsError:
                            pass
                        child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)
                        os.close(parent)
                        parent = child
                    mode = 0o755 if os.fstat(source.fileno()).st_mode & 0o111 else 0o644
                    fd = os.open(parts[-1], os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                                 mode, dir_fd=parent)
                    with os.fdopen(fd, "wb") as output:
                        shutil.copyfileobj(source, output)
                finally:
                    os.close(parent)
    finally:
        os.close(root)
        os.close(destination)


if __name__ == "__main__":
    try:
        main()
    except (OSError, UnicodeError, ValueError) as error:
        # Paths may be local; do not echo arbitrary contents or exception data.
        message = str(error) if isinstance(error, ValueError) and not isinstance(error, UnicodeError) else "missing, unsafe or unreadable manifest/source file"
        raise SystemExit("Source staging: " + message)
