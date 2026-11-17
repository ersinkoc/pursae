# World map

`world-110m.svg` is the Pursae Analytics country map (Pursae Modern admin theme): one `<path data-m="NNN">`
per country, keyed by its ISO 3166-1 numeric code, in an equirectangular projection (latitudes 84°…−58°,
Antarctica left out), 1000×470.

Source: Natural Earth 1:110m admin-0 countries (public domain) through the `world-atlas` 2.0.2 npm package
(ISC licence), `countries-110m.json`. Regenerate with:

    python3 tools/worldmap/topo2svg.py countries-110m.json web/admin/assets/maps/world-110m.svg
