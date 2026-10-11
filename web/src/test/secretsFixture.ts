// Throwaway fixture: a sops file encrypted to a key generated for this test only.
// Every value is fake. Shaped like GET /kbs/{kb}/secrets (internal/mcpserver/uiapi_secrets.go).
import type { SecretFile } from "../api/types";

export const FIXTURE_KEY =
  "AGE-SECRET-KEY-1RD0EEVHKJM8UZ9YV9GSLFRR05NWEUYGQTKHCR94AR4X6VKC20ESSCTFRK7";

export const FIXTURE_FILE: SecretFile = {
  "path": "secrets/demo.sops.yaml",
  "entries": [
    {
      "pointer": "/demo/username",
      "aad_path": "demo:username:",
      "enc": "ENC[AES256_GCM,data:0KUmSBC05Zym,iv:gmQoiNzvSajf5uRl0bWIfcO2eBrVDa5bkokY8sA51fI=,tag:gj+VJWCcyrNsV/6C5hazng==,type:str]",
      "referenced_by": [

      ]
    },
    {
      "pointer": "/demo/password",
      "aad_path": "demo:password:",
      "enc": "ENC[AES256_GCM,data:cidoFGDoJBZfTtYQVhmUo3D3Ya4=,iv:xGUMcjq3ccT6MhFJt4Vhpi4nG2+bCR0s9sBM0M/UZL0=,tag:EcaKhXwFTm17VqZ8k5jJNw==,type:str]",
      "referenced_by": [

      ]
    },
    {
      "pointer": "/demo/ports/0",
      "aad_path": "demo:ports:",
      "enc": "ENC[AES256_GCM,data:9i+0Dw==,iv:vEtSbJbBsnYAnB8SeqRqQO2DNPBSEUS3AY2WZxbqPu4=,tag:+Pe4LCxVgxW9QjZapfiY9Q==,type:int]",
      "referenced_by": [

      ]
    },
    {
      "pointer": "/demo/ports/1",
      "aad_path": "demo:ports:",
      "enc": "ENC[AES256_GCM,data:v4Awbg==,iv:K1/T3uvFpZZWSLApzz1p3qYzqIKI5ZD+idXOvzRPm08=,tag:1lIy+gqnLRgqQ6E3DjdrFQ==,type:int]",
      "referenced_by": [

      ]
    },
    {
      "pointer": "/demo/nested/a~1b",
      "aad_path": "demo:nested:a/b:",
      "enc": "ENC[AES256_GCM,data:iUsKmg==,iv:+Xb2FPkEc3FszrsYtKrnG1YvUXZ3POF4pOM8t/GpQMo=,tag:jWtlmf57b2OsTHGcoqaz6g==,type:bool]",
      "referenced_by": [

      ]
    },
    {
      "pointer": "/demo/nested/ratio",
      "aad_path": "demo:nested:ratio:",
      "enc": "ENC[AES256_GCM,data:cQr/,iv:JgN9YwUFfqG9et1aSuBxinIjY1dKP2l1jBt5zR4yq2Y=,tag:FNohAvVKNUPvWyIY4yT0Ow==,type:float]",
      "referenced_by": [

      ]
    },
    {
      "pointer": "/api_token",
      "aad_path": "api_token:",
      "enc": "ENC[AES256_GCM,data:sgDAP3R6M5X9CYopaSsS,iv:IiFteVL8dr7UqMeUdftC7gqGUTFLggr052qwdW+/ryk=,tag:UjJlgpLNfJeKcBFr4B70kA==,type:str]",
      "referenced_by": [

      ]
    }
  ],
  "recipients": [
    "age1lg4fxdeyt7gcaawhgg4mqyzkx4a8cp6z35cwfqz00zef8hfal32spnundq"
  ],
  "age_stanzas": [
    "-----BEGIN AGE ENCRYPTED FILE-----\nYWdlLWVuY3J5cHRpb24ub3JnL3YxCi0+IFgyNTUxOSBvTzlNZ05uWjlsNVhFZGpF\ncTRybUNjL2pUS3hlRTZ2aDJsMHY2OHozdXk0CnVNVE82YUdzaFZ4SUlIY1B2UlNE\nd0srZHl2aXo2QUNOMjlya3VxWXBOVzAKLS0tIHR4MlpGWDdhQ01RTEhueHk3ZkdC\nRHNlU0Z4YnArMm8xYVhtcUhOR042VXcKQh5dTSC145Cks37yzYiW3Em+URWjk8mi\nyZM5F/Js6jAf6VCDkubXSebIjhHX9Mu4TAMDgDxkniXjz6SfFEtRiA==\n-----END AGE ENCRYPTED FILE-----\n"
  ],
  "lastmodified": "2026-10-11T06:27:29Z",
  "source_of": [

  ]
};

export const FIXTURE_PLAIN: Record<string, string> = {
  "/demo/username": "fake-user",
  "/demo/password": "fake-pass/with:colon",
  "/demo/ports/0": "8080",
  "/demo/ports/1": "8443",
  "/demo/nested/a~1b": "True",
  "/demo/nested/ratio": "1.5",
  "/api_token": "fake-token-0000"
};
