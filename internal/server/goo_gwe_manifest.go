package server

// themeManifestRetired is embedded in themeManifest (theme_package.go): the old page builder's
// "golementor" key (a design file under golementor/). Only the old builder's files read it; this file
// goes with them, and so does the embedded field in themeManifest. The theme code ignores it.
type themeManifestRetired struct {
	Golementor string `json:"golementor"`
}
