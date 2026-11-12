# Gopher Board (ui-demo)

Example plugin for the admin and editor UI API of the Pursae plugin SDK:

- **Tools › Gopher Board** (`admin:pages`): an admin page whose form stores a message in plugin storage.
- **Dashboard widget** (`admin:dashboard`): shows that message.
- **Gopher Notes meta box** (`admin:metaboxes`): subtitle, mood and a flag on posts and pages,
  stored by the core as post meta `plugin_ui_demo_subtitle`, `plugin_ui_demo_mood`, `plugin_ui_demo_featured`.
- **Gopher Badge block** (`blocks:register`): a dynamic block with inspector controls, rendered on the server.
- **Gopher Badge Web Builder widget** (`builder:widgets`): the same badge for Pursae Web Builder.

Build: `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .`
or `tools/build-plugins.sh examples/plugins/ui-demo`.
