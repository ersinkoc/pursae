---
title: Themes with Pursae Web Builder
description: Design a whole site in Pursae Web Builder on top of Hello Pursae — Theme Builder templates, theme kits, imported HTML designs — and where this differs from a theme package.
weight: 82
section: themes
---

You do not have to write templates to have your own theme. Pursae Web Builder can design the
header, footer and every page kind visually, on top of the bundled **Hello Pursae** theme.

## Theme Builder templates

Under Web Builder › Theme Builder you design a **header**, a **footer** and templates for
**single** posts, **pages**, custom post types, the **front page**, **archives**, **search** and
**404**. They replace the theme's output on the pages they match
([Theme Builder guide](../web-builder-theme-builder.md), [Hello Pursae](hello-pursae.md)).

## Theme kits from the library

The theme library (editor › Studio › Items › Components › Browse theme library) imports a ready-made
full-site design: header, footer, every page template, demo pages and menus
([theme library](../web-builder-theme-library.md)). Import one and edit it like your own work.

## Importing your own HTML design

If you already have a finished HTML design, import it 1:1 as **code sections**: it keeps its own
markup, CSS and JavaScript from a design kit, and you can still edit texts, links and images and
reorder its parts in the builder. Upload the kit from the admin, or with the Web Builder REST API or
MCP tools ([code sections and design kits](../web-builder-code-sections.md)).

## What travels with a site

Templates, pages, kits and menus are site content: a backup or a migration of the site carries them
([backups](../operations/backups.md)). They stay on that site until you export the site.

## Limits

- Builder templates apply **only while Hello Pursae is active**. Switch to another theme and the site
  uses that theme's own templates.
- A builder theme is **not a package format**: you cannot zip it, upload it as a theme or list it on
  the Hub as a theme. For a theme others install, write a [theme package](your-first-theme.md).
- A theme package cannot receive Theme Builder parts.
- Keep the design in the builder or in your HTML source, and back the site up; there is no separate
  versioned file for it.
