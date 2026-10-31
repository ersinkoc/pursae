# Copies the Pro editor packages Pursae Web Builder's atomic editor needs from an upstream Pro 4.3.0 copy
# (SRC) into web/gowebeditor/pro/js and strips promotions / remote assets (see NOTICE).
import shutil, sys, os
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '../..')
# ELEMENTOR_PRO_SRC overrides the default: wp-assets/elementor-pro next to the repository.
SRC = os.path.join(os.environ.get('ELEMENTOR_PRO_SRC', os.path.join(ROOT, '..', 'wp-assets', 'elementor-pro')), 'assets/js')
DST = os.path.join(ROOT, 'web/gowebeditor/pro/js')
PKGS = ['core-adapter-utils', 'editor-canvas-extended', 'editor-templates-extended', 'editor-controls-extended',
        'editor-editing-panel-extended', 'editor-components-extended']
for p in PKGS:
    os.makedirs(f'{DST}/packages/{p}', exist_ok=True)
    shutil.copy(f'{SRC}/packages/{p}/{p}.min.js', f'{DST}/packages/{p}/{p}.min.js')
shutil.copy(f'{SRC}/atomic-form-editor.min.js', f'{DST}/atomic-form-editor.min.js')

def patch(path, reps):
    s = open(path).read()
    for old, new in reps:
        n = s.count(old)
        if n != 1:
            sys.exit(f'{path}: {n} matches for {old[:80]!r}')
        s = s.replace(old, new)
    open(path, 'w').write(s)

patch(f'{DST}/packages/editor-components-extended/editor-components-extended.min.js', [
    # AI assistant (Angie) promotion: never shown
    ('function canShowAngieElementorPromotion(){', 'function canShowAngieElementorPromotion(){return!1;'),
    # remote intro picture
    ('f.createElement(_.Image,{sx:{width:`296px`,height:`160px`},src:`https://assets.elementor.com/packages/v1/images/components-properties-intro.png`,alt:``}),', ''),
    # "Learn more" links to the upstream docs
    ('f.createElement(_.Link,{href:`http://go.elementor.com/components-guide`,target:`_blank`,sx:{mt:2},color:`info.main`,variant:`body2`},(0,d.__)(`Learn more`,`elementor-pro`)),', ''),
    (',f.createElement(_.Link,{href:Ie,target:`_blank`,rel:`noopener noreferrer`,variant:`caption`,color:`info.main`},(0,d.__)(`Learn more`,`elementor-pro`))', ''),
])
patch(f'{DST}/atomic-form-editor.min.js', [
    # elementor.config.widgets is deprecated (console warning on every call): read the widgets cache
    ('(e=e.config)===null||e===void 0?void 0:e.widgets)', '(e=e.widgetsCache)===null||e===void 0?void 0:e)'),
])
patch(f'{DST}/packages/editor-controls-extended/editor-controls-extended.min.js', [
    # the code editor (custom CSS) is served locally, not from a CDN
    ('{paths:{vs:`https://cdn.jsdelivr.net/npm/monaco-editor@0.55.1/min/vs`}}',
     '{paths:{vs:((window.elementorCommon&&elementorCommon.config&&elementorCommon.config.urls&&elementorCommon.config.urls.assets)||"/wp-content/plugins/gowebeditor/assets/")+"lib/monaco-editor/min/vs"}}'),
])
print('ok')
