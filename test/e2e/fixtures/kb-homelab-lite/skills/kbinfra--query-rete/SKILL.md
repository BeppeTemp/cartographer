---
name: kbinfra--query-rete
description: Guide for querying the infra map (rete expanded concept) of the homelab KB.
version: "1.0"
---
# KB Infra — Network Query

## Purpose

Helps the agent find information about the homelab's network infrastructure.

## Steps

1. Use `search` with query "gateway" or "dns" or "network" to find relevant concepts.
2. Read the concepts found with `concept_read` to extract details.
3. The reference map is `infra`, expanded concept `rete`.

## Key concepts

- `infra/rete/gateway` — network gateway (IP, configuration)
- `infra/rete/dns` — internal DNS server

## Notes

- Concepts use the types `service` (network services) and `note` (notes).
- Emergent ontology: new types are registered automatically.
