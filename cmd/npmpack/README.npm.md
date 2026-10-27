# pgEdge CLI

`pgedge` is the command-line interface for the pgEdge product suite.
It manages pgEdge Starfleet databases and infrastructure, and a
self-hosted pgEdge Control Plane, from one binary.

This package installs the native binary for your platform. Install it
globally, or run it once with `npx`:

```sh
npm install -g {{spec}}
npx {{spec}} --help
```

Update it through npm the same way you installed it; `pgedge self
update` refuses an npm install.

To give your AI agent the pgedge skills in every project, install them
for the release this package carries:

```sh
npx -y skills add 'pgEdge/pgedge-cli#{{release}}' --global
```

The documentation, the other install routes and the source are at
[github.com/pgEdge/pgedge-cli](https://github.com/pgEdge/pgedge-cli).
