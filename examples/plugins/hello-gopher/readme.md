# Hello Gopher

Demo plugin for WordPress Go.

- Filter `the_content`: appends "Served by a Wasm plugin" to single posts when
  the `hello_gopher_footer` option is truthy (`1`, `true`, `yes`, `on`).
- Action `admin_notices`: shows a random gopher one-liner on the dashboard.
