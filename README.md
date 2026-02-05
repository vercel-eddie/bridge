# Bridge

A set of applications that empowers developers on the Vercel platform to write their code locally, sync their
files to Vercel Sandbox, and forward/recieve traffic made from/to a Vercel Preview deployment.

## API

The API is defined using protocol buffers. Currently, all messages are sent/received via websocket/HTTP but the payloads
themselves are housed within the [protos directory](./proto).

### Generate

To generate, install [buf](https://buf.build/docs/cli/installation/) and run:

```
make
```

## Architecture

See [here](./docs/architecture.md) for more info.
