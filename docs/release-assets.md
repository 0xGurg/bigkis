# Release Assets

The bootstrap installer at
[install.sh](https://github.com/0xGurg/bigkis/raw/refs/heads/main/install.sh)
expects prebuilt release assets named:

- `bigkis-linux-amd64`
- `bigkis-linux-arm64`
- `checksums.txt`

`checksums.txt` should contain standard SHA256 lines:

```text
<sha256>  bigkis-linux-amd64
<sha256>  bigkis-linux-arm64
```

`install.sh` is fail-closed: if `checksums.txt` cannot be downloaded, has no
entry for the requested asset, or does not match, the install aborts. To
bypass verification (not recommended), set `BIGKIS_INSECURE=1`.

## Optional: minisign signature on `checksums.txt`

When `MINISIGN_PRIVATE_KEY` (and optionally `MINISIGN_PASSWORD`) are set as
repository secrets, the release workflow signs `checksums.txt` and uploads
`checksums.txt.minisig` alongside it. To verify after download:

```sh
minisign -V -P "<your minisign public key>" \
  -m checksums.txt -x checksums.txt.minisig
```

Publishing the signature is optional; without the secrets, the release
workflow still produces `checksums.txt` and the SHA256 verification in
`install.sh` continues to work.

## How releases are produced

Releases are produced by the
[release workflow](https://github.com/0xGurg/bigkis/blob/main/.github/workflows/release.yml),
which runs when you **push a version tag** matching `v*` (for example
`v0.7.7`). The workflow builds static `linux/amd64` and `linux/arm64` binaries
with `CGO_ENABLED=0`, generates `checksums.txt`, optionally signs it with
minisign, and publishes the result as GitHub release assets.

### Cutting a release

1. Land your changes on `main`.

2. Tag that commit and push the tag:

   ```sh
   git checkout main
   git pull
   git tag -a v0.7.7 -m "v0.7.7"
   git push origin v0.7.7
   ```

3. Watch the run on
   [Actions](https://github.com/0xGurg/bigkis/actions) and verify the assets at
   [Releases](https://github.com/0xGurg/bigkis/releases).

Release notes are auto-generated from merged pull requests. You can also edit
the release body manually on the GitHub releases page after the workflow
completes.
