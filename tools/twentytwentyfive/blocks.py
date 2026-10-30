"""Block markup helpers for the Twenty Twenty-Five demo content (demos.py): each returns the block's
serialized form as the block editor saves it."""

import json


def _attrs(a):
    return (' ' + json.dumps(a, separators=(',', ':'), ensure_ascii=False)) if a else ''


def _media_attrs(a):
    # "id" carries a {{media_id:x}} token, which must stay unquoted in the JSON.
    s = _attrs(a)
    return s.replace('"@@', '').replace('@@"', '')


def mid(name):
    return '@@{{media_id:%s}}@@' % name


def p(html, align=None, size=None, cls=None):
    a, c = {}, []
    if align:
        a['align'] = align
        c.append('has-text-align-' + align)
    if cls:
        a['className'] = cls
        c.append(cls)
    if size:
        a['fontSize'] = size
        c.append('has-%s-font-size' % size)
    klass = (' class="%s"' % ' '.join(c)) if c else ''
    return '<!-- wp:paragraph%s -->\n<p%s>%s</p>\n<!-- /wp:paragraph -->' % (_attrs(a), klass, html)


def h(text, level=2, align=None, size=None, cls=None):
    a, c = {}, ['wp-block-heading']
    if level != 2:
        a['level'] = level
    if align:
        a['textAlign'] = align
        c.append('has-text-align-' + align)
    if cls:
        a['className'] = cls
        c.append(cls)
    if size:
        a['fontSize'] = size
        c.append('has-%s-font-size' % size)
    return '<!-- wp:heading%s -->\n<h%d class="%s">%s</h%d>\n<!-- /wp:heading -->' % (_attrs(a), level, ' '.join(c), text, level)


def img(media, alt, caption='', size='large', align=None):
    a = {'id': mid(media), 'sizeSlug': size, 'linkDestination': 'none'}
    c = ['wp-block-image', 'size-' + size]
    if align:
        a['align'] = align
        c.insert(1, 'align' + align)
    cap = '<figcaption class="wp-element-caption">%s</figcaption>' % caption if caption else ''
    return '<!-- wp:image%s -->\n<figure class="%s"><img src="{{media:%s}}" alt="%s" class="wp-image-{{media_id:%s}}"/>%s</figure>\n<!-- /wp:image -->' % (
        _media_attrs(a), ' '.join(c), media, alt, media, cap)


def quote(text, cite=''):
    c = '<cite>%s</cite>' % cite if cite else ''
    return '<!-- wp:quote -->\n<blockquote class="wp-block-quote">%s%s</blockquote>\n<!-- /wp:quote -->' % (p(text), c)


def ul(items):
    li = ''.join('<!-- wp:list-item -->\n<li>%s</li>\n<!-- /wp:list-item -->' % i for i in items)
    return '<!-- wp:list -->\n<ul class="wp-block-list">%s</ul>\n<!-- /wp:list -->' % li


def ol(items):
    li = ''.join('<!-- wp:list-item -->\n<li>%s</li>\n<!-- /wp:list-item -->' % i for i in items)
    return '<!-- wp:list {"ordered":true} -->\n<ol class="wp-block-list">%s</ol>\n<!-- /wp:list -->' % li


def sep():
    return '<!-- wp:separator -->\n<hr class="wp-block-separator has-alpha-channel-opacity"/>\n<!-- /wp:separator -->'


def spacer(size='50'):
    return ('<!-- wp:spacer {"height":"var:preset|spacing|%s"} -->\n<div style="height:var(--wp--preset--spacing--%s)" aria-hidden="true" class="wp-block-spacer"></div>\n<!-- /wp:spacer -->'
            % (size, size))


def buttons(items, align=None):
    """items: [(label, href, outline)]"""
    out = []
    for label, href, outline in items:
        if outline:
            out.append('<!-- wp:button {"className":"is-style-outline"} -->\n<div class="wp-block-button is-style-outline"><a class="wp-block-button__link wp-element-button" href="%s">%s</a></div>\n<!-- /wp:button -->' % (href, label))
        else:
            out.append('<!-- wp:button -->\n<div class="wp-block-button"><a class="wp-block-button__link wp-element-button" href="%s">%s</a></div>\n<!-- /wp:button -->' % (href, label))
    a, c = {}, ['wp-block-buttons']
    if align:
        a['layout'] = {'type': 'flex', 'justifyContent': align}
        c.append('is-content-justification-' + align)
    return '<!-- wp:buttons%s -->\n<div class="%s">%s</div>\n<!-- /wp:buttons -->' % (_attrs(a), ' '.join(c), ''.join(out))


def columns(cols, align='wide', gap=None):
    a = {}
    if align:
        a['align'] = align
    c = ['wp-block-columns'] + (['align' + align] if align else [])
    inner = ''.join('<!-- wp:column -->\n<div class="wp-block-column">%s</div>\n<!-- /wp:column -->' % ''.join(col) for col in cols)
    return '<!-- wp:columns%s -->\n<div class="%s">%s</div>\n<!-- /wp:columns -->' % (_attrs(a), ' '.join(c), inner)


def group(inner, align=None, section=None, pad='60'):
    a, c, style = {}, ['wp-block-group'], ''
    if align:
        a['align'] = align
        c.append('align' + align)
    if section:
        a['className'] = 'is-style-' + section
        c.append('is-style-' + section)
    if pad:
        sp = {'top': 'var:preset|spacing|' + pad, 'bottom': 'var:preset|spacing|' + pad}
        css = 'padding-top:var(--wp--preset--spacing--%s);' % pad
        if align == 'full':
            sp['left'] = sp['right'] = 'var:preset|spacing|50'
            css += 'padding-right:var(--wp--preset--spacing--50);'
        css += 'padding-bottom:var(--wp--preset--spacing--%s)' % pad
        if align == 'full':
            css += ';padding-left:var(--wp--preset--spacing--50)'
        a['style'] = {'spacing': {'padding': sp}}
        style = ' style="%s"' % css
    a['layout'] = {'type': 'constrained'}
    return '<!-- wp:group%s -->\n<div class="%s"%s>%s</div>\n<!-- /wp:group -->' % (_attrs(a), ' '.join(c), style, ''.join(inner))


def cover(media, inner, height=620, dim=40):
    a = {'url': '{{media:%s}}' % media, 'id': mid(media), 'dimRatio': dim, 'minHeight': height, 'minHeightUnit': 'px',
         'align': 'full', 'layout': {'type': 'constrained'}}
    return ('<!-- wp:cover%s -->\n<div class="wp-block-cover alignfull" style="min-height:%dpx"><img class="wp-block-cover__image-background wp-image-{{media_id:%s}}" alt="" src="{{media:%s}}" data-object-fit="cover"/>'
            '<span aria-hidden="true" class="wp-block-cover__background has-background-dim-%d has-background-dim"></span><div class="wp-block-cover__inner-container">%s</div></div>\n<!-- /wp:cover -->'
            % (_media_attrs(a), height, media, media, dim, ''.join(inner)))


def gallery(medias, columns_n=3, align='wide'):
    """medias: [(media, alt)]"""
    imgs = ''.join(img(m, alt) for m, alt in medias)
    a = {'columns': columns_n, 'linkTo': 'none', 'align': align}
    return '<!-- wp:gallery%s -->\n<figure class="wp-block-gallery align%s has-nested-images columns-%d is-cropped">%s</figure>\n<!-- /wp:gallery -->' % (
        _attrs(a), align, columns_n, imgs)


def latest_posts(n=4):
    return '<!-- wp:latest-posts {"postsToShow":%d,"displayPostDate":true,"align":"wide"} /-->' % n


def card(media, alt, title, text, href, link_label):
    return [img(media, alt), h('<a href="%s">%s</a>' % (href, title), 3, size='large'), p(text), p('<a href="%s">%s</a>' % (href, link_label), size='small')]


def blocks(*parts):
    return '\n\n'.join(parts)
