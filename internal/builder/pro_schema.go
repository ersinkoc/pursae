package builder

// Golementor Pro: the control schema dumped from upstream 4.3.5 with upstream Pro 4.3.0 active
// (testdata/tools/dump-controls.php in the lab). It supersedes data/controls.json.gz: it has every
// free stack plus Pro's widgets and the Pro controls on sections, columns, containers, pages and
// the kit (motion effects, sticky, custom CSS, attributes, display conditions, scroll snap).

import _ "embed"

//go:embed data/pro-controls.json.gz
var proControlsGz []byte
