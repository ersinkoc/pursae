# Pursae Web Builder Studio library

Ready sections and site kits for the Studio panel, the classic template library (Blocks tab) and the
setup wizard (`/wp-admin/golementor-setup`). Formats and rules: `docs/golementor-studio.md`, section 3.
The server side is `internal/server/golementor_library.go` (loading, validation, REST) and
`internal/server/golementor_setup.go` (the wizard).

    sections/<category>/<slug>.json   one section, with <slug>.webp (600px wide) next to it
    kits/<slug>/kit.json              one starter site, with its screenshot next to it
    images/                           shared placeholder artwork, referenced as {{library:images/<file>}}

Files here are served at `/wp-content/plugins/gowebeditor/assets/library/`. Invalid files are left
out of the library and logged; `go test -run TestGolementorLibraryContent ./internal/server/` lists
every problem.
