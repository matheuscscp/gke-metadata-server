# Copyright 2026 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

# Proves that the google-auth Python library needs DNS resolution of
# metadata.google.internal. Detection (ping) targets the IP and works in both
# pods. Metadata fetches default to the hostname metadata.google.internal, so
# they work only when hostAliases maps that name to the metadata IP. EXPECT_DNS
# selects which outcome this pod asserts.

import os
import sys

import google.auth.transport.requests as treq
from google.auth.compute_engine import _metadata


def main():
    expect_dns = os.environ.get("EXPECT_DNS", "")
    req = treq.Request()

    # Detection probes the IP, so it must succeed regardless of DNS.
    on_gce = _metadata.ping(req)
    print(f"ping over the IP        : {on_gce}", flush=True)
    if not on_gce:
        print("FAIL: ping over the IP must succeed on a routing node", flush=True)
        return 1

    # Fetches default to the hostname metadata.google.internal, so they need DNS.
    fetch_ok = False
    try:
        project = _metadata.get(req, "project/project-id")
        fetch_ok = True
        print(f"fetch over the hostname : ok, project-id={project}", flush=True)
    except Exception as e:
        print(f"fetch over the hostname : failed, {type(e).__name__}: {e}", flush=True)

    if expect_dns == "yes":
        if fetch_ok:
            print("PASS: hostAliases present, the hostname fetch works", flush=True)
            return 0
        print("FAIL: expected the hostname fetch to succeed with hostAliases", flush=True)
        return 1
    if expect_dns == "no":
        if not fetch_ok:
            print("PASS: no hostAliases, the hostname fetch fails, DNS is required", flush=True)
            return 0
        print("FAIL: expected the hostname fetch to fail without hostAliases", flush=True)
        return 1
    print(f"FAIL: EXPECT_DNS must be yes or no, got {expect_dns!r}", flush=True)
    return 1


if __name__ == "__main__":
    sys.exit(main())
