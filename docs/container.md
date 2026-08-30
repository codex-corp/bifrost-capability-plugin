# GHCR runtime image

The container image is an additional distribution path for the same tested ABI pair published in the GitHub Release.

It contains:

- the matched `bifrost-http` executable
- `agent-capability-router.so`
- release compatibility metadata
- the isolated candidate test result

It does **not** inject provider credentials, create Virtual Keys, or silently mutate an existing Bifrost configuration.

## Pull

Use the exact release tag for production deployments:

```bash
docker pull ghcr.io/codex-corp/bifrost-capability-plugin:bifrost-v2.0.0-r1
```

Convenience aliases are also published for the newest `bifrost-v2.0.0-rN` release:

```text
bifrost-v2.0.0
latest
```

Publishing or re-running an older release only updates its exact release tag; it does not roll the mutable aliases backward.

Prefer the versioned release tag, or pin the resulting image digest when you need immutable deployment identity.

After the first publication, confirm the package visibility in GitHub Packages. Anonymous `docker pull` works only when the GHCR package is public; otherwise authenticate to GHCR or change the package visibility to Public.

## Run

Persist the Bifrost app directory outside the container:

```bash
docker run --name bifrost-capability-router \
  -p 127.0.0.1:10020:8080 \
  -v "$HOME/.config/bifrost:/app/data" \
  ghcr.io/codex-corp/bifrost-capability-plugin:bifrost-v2.0.0-r1
```

The gateway stores configuration and logs under `/app/data`. The capability plugin is available at:

```text
/opt/bifrost/plugins/agent-capability-router.so
```

After the container is healthy, register that path through the Bifrost Dashboard or the repository tooling, using the same plugin configuration and ordering documented for the binary release.

Do not point Bifrost at a `.so` from another release.

## Verify

```bash
curl -fsS http://127.0.0.1:10020/health
curl -fsS http://127.0.0.1:10020/api/version
```

Inspect the packaged compatibility evidence if needed:

```bash
docker run --rm --entrypoint cat \
  ghcr.io/codex-corp/bifrost-capability-plugin:bifrost-v2.0.0-r1 \
  /opt/bifrost/compatible-runtime.json
```

## Existing release

The `Publish GHCR Runtime` GitHub Actions workflow accepts an existing release tag. It downloads all checksum-covered release assets, verifies `SHA256SUMS`, extracts the already-tested matched artifacts, resolves the source commit behind the release tag, builds the runtime image, and publishes it to GHCR.

This is the correct path for `bifrost-v2.0.0-r1`, because it avoids rebuilding the Bifrost/plugin ABI pair from newer source. The exact image tag is always published; `bifrost-v2.0.0` and `latest` are updated only when the selected release is the newest matching `rN` release.

## Future releases

The normal `Release` workflow publishes both outputs after the ABI-matched candidate test succeeds:

1. GitHub Release assets for raw binary installation.
2. A GHCR runtime image containing the same matched host/plugin pair.

Published image tags are:

```text
ghcr.io/codex-corp/bifrost-capability-plugin:<release-tag>
ghcr.io/codex-corp/bifrost-capability-plugin:bifrost-v2.0.0
ghcr.io/codex-corp/bifrost-capability-plugin:latest
```

The version and `latest` aliases track only the newest matching release; older release reruns cannot move them backward.
