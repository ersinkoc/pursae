# gap-loops: copies upstream Pro 4.3.0's editor-collection-loop package (the atomic Loop element's
# editor UI) into web/gowebeditor/pro/js/packages and removes its license check (see NOTICE).
import os, shutil, sys
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '../..')
# ELEMENTOR_PRO_SRC overrides the default: wp-assets/elementor-pro next to the repository.
SRC = os.path.join(os.environ.get('ELEMENTOR_PRO_SRC', os.path.join(ROOT, '..', 'wp-assets', 'elementor-pro')), 'assets/js/packages/editor-collection-loop')
DST = os.path.join(ROOT, 'web/gowebeditor/pro/js/packages/editor-collection-loop')
os.makedirs(DST, exist_ok=True)
s = open(f'{SRC}/editor-collection-loop.min.js').read()
old = '(yield(0,a.fetchLicenseStatus)().catch(()=>!1))'
if s.count(old) != 1:
    sys.exit('license check not found')
s = s.replace(old, '!1')
open(f'{DST}/editor-collection-loop.min.js', 'w').write(s)
print('ok')
