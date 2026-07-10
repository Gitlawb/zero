# Plugin Marketplace

Zero's plugin marketplace is local-first. Catalogs are JSON files that describe
plugin releases, signed by an optional detached Ed25519 `catalog.sig`.

## Catalogs

Register catalogs with:

```bash
zero plugins marketplace validate ./catalog.json
zero plugins marketplace add ./catalog.json --allow-unverified
zero plugins marketplace list
```

User catalogs are stored in `~/.config/zero/marketplaces.json`. Project catalogs
are stored in `<workspace>/.zero/marketplaces.json`.

## Catalog Format

`catalog.json` has schema version `1`, a catalog id, owner, and a `plugins`
array. Each plugin has curated review metadata and one or more immutable
releases:

```json
{
  "schemaVersion": 1,
  "id": "team",
  "owner": "Platform",
  "plugins": [
    {
      "id": "zero.demo",
      "name": "Zero Demo",
      "author": {"name": "Platform"},
      "license": "MIT",
      "review": {"status": "community"},
      "releases": [
        {
          "version": "0.1.0",
          "repository": "https://github.com/Gitlawb/zero-demo-plugin.git",
          "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "treeHash": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
          "components": {
            "tools": [{"name": "lookup", "permission": "prompt"}],
            "hooks": [{"name": "preflight", "event": "beforeTool"}]
          }
        }
      ]
    }
  ]
}
```

Supported hook events are exactly `beforeTool`, `afterTool`, `sessionStart`,
and `sessionEnd`. Specialist hook events are rejected by catalog validation.

## Install Safety

Marketplace installs require `--yes` and verify the fetched plugin against
catalog metadata before publishing:

```bash
zero plugins browse lookup --catalog team
zero plugins install zero.demo@team --yes
```

Install checks include plugin id, manifest version, tree hash, component names,
tool permissions, and hook events. Managed plugins are recorded in `plugins.lock`
with catalog, version, commit, subdir, hash, pinned, and enabled state.

Disabled managed plugins move to `<plugins-root>/.disabled/<id>`. The loader
lists quarantined plugins but never activates them; a disabled project plugin
still shadows a same-id user plugin.
