# Vercel Remote IDEs

A simple Go CLI application built with [urfave/cli](https://cli.urfave.org/) that allows you to leverage the persistent
filesystem of Vercel sandboxes to develop seamlessly with coding agents:

## Order of operations

1. Spins up a Vercel sandbox.
2. Installs a server-side SSH server in the Sandbox that opens up an HTTP CONNECT tunnel as well as an SSH server.
3. Exposes the above tunnel via a port to a client.
4. Client connects to the exposed endpoint via SSH.
5. When the client session closes, a snapshot of the Sandbox is taken, and everything is cleaned up.
