#!/usr/bin/env python3
"""从 CHANGELOG.md 提取版本说明，并追加发布制品信息。"""

import os
import sys
from pathlib import Path

TAG = sys.argv[1]
CHANGELOG = Path(__file__).resolve().parent.parent / "CHANGELOG.md"
lines = CHANGELOG.read_text(encoding="utf-8").splitlines()

heading = f"## {TAG}"
start = None
for index, line in enumerate(lines):
    if line.strip() == heading:
        start = index
        break
if start is None:
    sys.exit(f"CHANGELOG.md 中找不到版本 {TAG} 的变更记录")

end = len(lines)
for index in range(start + 1, len(lines)):
    if lines[index].startswith("## "):
        end = index
        break

body = "\n".join(lines[start + 1 : end]).strip()
parts = [body, "", "## 发布制品", ""]
commit = os.environ.get("GITHUB_SHA", "")
repo = os.environ.get("GITHUB_REPOSITORY", "theriseunion-edge/edge-cli")
tag = os.environ.get("GITHUB_REF_NAME", TAG)
oci = os.environ.get("OCI_REFERENCE", "")
digest = os.environ.get("OCI_DIGEST", "")
checksums = os.environ.get("CHECKSUMS", "")
if commit:
    parts.append(f"- 源码提交：`{commit}`")
if oci:
    parts.append(f"- OCI 制品：`{oci}`")
if digest:
    parts.append(f"- OCI 摘要：`{digest}`")
parts.append(f"- GitHub Release：https://github.com/{repo}/releases/tag/{tag}")
if checksums:
    parts.extend(["", "## 校验值", "", "```text", checksums.strip(), "```"])
print("\n".join(parts))
