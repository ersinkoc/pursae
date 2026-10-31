-- Pursae rename (WordPress Go -> Pursae). The builder's base theme moved from hello-gowebeditor to
-- hello-web-builder; settings that still hold an old default text get the new one.

UPDATE settings SET value = '"hello-web-builder"'
 WHERE key IN ('template', 'stylesheet') AND value = '"hello-gowebeditor"';

UPDATE settings SET value = '"Hello Pursae Web Builder"'
 WHERE key = 'current_theme' AND value = '"Hello Golementor"';

UPDATE settings SET key = 'theme_mods_hello-web-builder'
 WHERE key = 'theme_mods_hello-gowebeditor'
   AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'theme_mods_hello-web-builder');

UPDATE settings SET value = '"Just another Pursae site"'
 WHERE key = 'blogdescription' AND value = '"Just another WordPress Go site"';
