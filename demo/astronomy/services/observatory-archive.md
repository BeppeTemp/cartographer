---
type: Service
title: Observatory image archive
description: The club's archive of raw frames.
kind: storage
base_url: https://archive.example.com/
status: active
secret_refs: [ARCHIVE_USER=secrets/observatory.sops.yaml#/archive/user, ARCHIVE_PASSWORD=secrets/observatory.sops.yaml#/archive/password]
---

# Observatory image archive

Raw frames from [[observing/photographing-the-milky-way]] sessions are uploaded here before stacking.
