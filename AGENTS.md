# Debugging

When debugging what's going on in a fullstack test, there are 3 components to look at:

1. Devcontainer: This is running locally in Docker. Use standard docker commands to see what's going on.
2. Bridge server: This is running in a Vercel Sandbox. Use the `sandbox` CLI in
   the [userservice](./services/userservice) directory to get the logs
3. Dispatcher: This is running in a Vercel preview deployment. Use the `Vercel` CLI in the userservice directory.

The current Function URL and Sandbox URL can be found in the bridge feature
of [Devcontainer](./services/userservice/.devcontainer/userservice/devcontainer.json).
