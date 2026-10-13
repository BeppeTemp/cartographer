---
type: Service
title: Minor Planet Center
description: The IAU clearinghouse for asteroid and comet observations.
kind: api
base_url: https://minorplanetcenter.net/
status: active
secret_refs: [MPC_API_TOKEN=secrets/observatory.sops.yaml#/mpc/token]
---

# Minor Planet Center

Where positions of asteroids and [[solar-system/comets]] are submitted and orbits are published. Submitting needs the token declared in `secret_refs`.
