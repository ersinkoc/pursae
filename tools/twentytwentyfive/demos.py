"""Builds the demo content sets of Twenty Twenty-Five (web/themes/twentytwentyfive/demos/<set>/):
demo.json (docs/theme-package.md, "Demo content") and the images, taken from the theme's own CC0
photographs (assets/images, rawpixel.com public domain), resized and compressed to WebP, plus the
chooser's preview image (assets/demos/<set>.webp).

    python3 tools/twentytwentyfive/demos.py <twentytwentyfive dir> <web/themes/twentytwentyfive>

Needs ImageMagick (magick). The texts are original.
"""

import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from blocks import (blocks, buttons, card, columns, cover, gallery, group, h, img, latest_posts, ol, p, quote, sep,
                    spacer, ul)

SRC, OUT = sys.argv[1], sys.argv[2]


def media(mid, src, alt, width=1600, title=''):
    return {'id': mid, 'src': src, 'width': width, 'alt': alt, 'title': title or alt}


def post(slug, title, date, cats, tags, feat, body, excerpt='', comments=None, sticky=False):
    d = {'slug': slug, 'title': title, 'date': date, 'categories': cats, 'tags': tags, 'featured_media': feat, 'content': body}
    if excerpt:
        d['excerpt'] = excerpt
    if comments:
        d['comments'] = comments
    if sticky:
        d['sticky'] = True
    return d


def comment(author, text, date, email=''):
    c = {'author': author, 'content': text, 'date': date}
    if email:
        c['email'] = email
    return c


SETS = []

# 1. Wildflower Journal: a nature and travel blog ------------------------------------------------------

SETS.append({
    'slug': 'wildflower-journal',
    'name': 'Wildflower Journal',
    'description': 'A nature and travel blog: meadows, creeks and quiet trails, with field notes and walking guides.',
    'preview': 'hero',
    'options': {'blogname': 'Wildflower Journal', 'blogdescription': 'Field notes from meadows, creeks and quiet trails', 'posts_per_page': 6},
    'customizer': {'color_scheme': 'default'},
    'media': [
        media('hero', 'northern-buttercups-flowers.webp', 'A field of northern buttercups in full bloom'),
        media('meadow', 'coming-soon-bg-image.webp', 'A wildflower meadow in Llano, Texas, under a pale sky'),
        media('creek', 'dallas-creek-square.webp', 'A shallow creek running over stones between grassy banks', 594),
        media('birds', 'marshland-birds-square.webp', 'Marshland birds wading in the shallows of a wildlife refuge', 594),
        media('cliff', 'poster-image-background.webp', 'Cliff Palace, stone dwellings under a sandstone overhang', 1200),
        media('coral', 'coral-square.webp', 'Coral and small fish in clear, shallow water', 594),
        media('meadow-square', 'flower-meadow-square.webp', 'Close view of a meadow with orange and yellow wildflowers', 594),
        media('ridge', '404-image.webp', 'A small totara tree alone on a grassy ridge', 1048),
    ],
    'categories': [
        {'slug': 'meadows', 'name': 'Meadows', 'description': 'Wildflowers, grasses and the insects that live among them.'},
        {'slug': 'waterways', 'name': 'Waterways', 'description': 'Creeks, marshes and the shallow edges of the sea.'},
        {'slug': 'trails', 'name': 'Trails', 'description': 'Walks worth getting up early for.'},
        {'slug': 'field-guides', 'name': 'Field Guides', 'description': 'Practical notes for curious walkers.'},
    ],
    'tags': [{'slug': s, 'name': n} for s, n in [('spring', 'Spring'), ('wildflowers', 'Wildflowers'), ('birds', 'Birds'), ('slow-travel', 'Slow travel'),
                                                   ('photography', 'Photography'), ('conservation', 'Conservation'), ('maps', 'Maps')]],
    'pages': [
        {'slug': 'home', 'title': 'Home', 'front_page': True, 'template': 'page-no-title', 'content': blocks(
            cover('hero', [
                p('Field notes, season by season', size='medium'),
                h('Slow walks through wild places', 1, size='xx-large'),
                p('Meadows in bloom, creeks after rain, birds at first light. A journal for anyone who likes to stop and look a little longer.', size='large'),
                buttons([('Read the journal', '{{page:journal}}', False), ('About this journal', '{{page:about}}', True)]),
            ], height=640, dim=30),
            group([
                h('What you will find here', align='center'),
                p('Every post starts outside: a morning on a trail, an afternoon on a creek bank, a few hours with a notebook in the grass. We write down what we see, how to get there, and how to leave it as we found it.', align='center', size='medium'),
            ]),
            columns([
                card('meadow-square', 'Wildflowers in a meadow', 'Meadows', 'When the buttercups return, which flowers follow, and why mowing late matters.', '{{category:meadows}}', 'Read about meadows &rarr;'),
                card('creek', 'A shallow creek', 'Waterways', 'Reading a creek, counting birds at the marsh and a morning on the reef.', '{{category:waterways}}', 'Read about waterways &rarr;'),
                card('ridge', 'A tree on a ridge', 'Trails', 'Walks we return to, with maps, timings and the best light of the day.', '{{category:trails}}', 'Read about trails &rarr;'),
            ]),
            group([
                quote('In every walk with nature one receives far more than he seeks.', 'John Muir'),
            ], align='full', section='section-1'),
            group([
                h('Latest from the journal'),
                latest_posts(4),
                buttons([('All posts', '{{page:journal}}', True)]),
            ]),
        )},
        {'slug': 'journal', 'title': 'Journal', 'posts_page': True, 'content': ''},
        {'slug': 'about', 'title': 'About', 'featured_media': 'meadow', 'content': blocks(
            p('Wildflower Journal began as a notebook kept in a coat pocket: dates, places, what was flowering, which birds were calling. After a few years the notebook had become a map of the seasons, and it seemed a shame to keep it in a drawer.', size='large'),
            p('We are two walkers, a pair of boots each and one shared pair of binoculars. We are not botanists or ornithologists, just people who learned the names of things by looking them up, one walk at a time. If we get a name wrong, tell us; we will fix it and thank you.'),
            h('How we walk'),
            ul(['We stay on paths, and close every gate behind us.', 'We photograph flowers instead of picking them.', 'We go back to the same places in every season, because a meadow in March and a meadow in June are two different places.']),
            h('Get in touch'),
            p('Questions, corrections and walk suggestions are always welcome on the <a href="{{page:contact}}">contact page</a>.'),
        )},
        {'slug': 'contact', 'title': 'Contact', 'content': blocks(
            p('We read every message, usually on a Sunday evening with the week&rsquo;s notes spread out on the table.', size='large'),
            columns([
                [h('Write to us', 3), p('<a href="mailto:hello@example.com">hello@example.com</a>'), p('Walk suggestions, corrections and questions about a route are all welcome.')],
                [h('Walk with us', 3), p('Once a month we lead a free, slow walk of about two hours. Dates are announced in the journal.'), p('Bring water, sturdy shoes and something to write with.')],
            ], align=None),
        )},
    ],
    'posts': [
        post('the-week-the-buttercups-came-back', 'The week the buttercups came back', '2026-04-18T08:30:00', ['meadows'], ['spring', 'wildflowers'], 'hero', blocks(
            p('For most of the winter the lower field is a flat, wet green. Then, sometime in April, it changes in a single week. On Monday there were a few yellow points along the ditch; by Saturday the whole slope was gold.'),
            p('Buttercups are easy to overlook because they are everywhere, but it is worth kneeling down for one. The petals have a glassy shine that seems lit from inside: a layer of cells just under the surface reflects light back out, which is why a buttercup held under your chin throws a yellow glow.'),
            h('What comes next'),
            p('The buttercups are the start of a sequence. In the same field we usually see, in order:'),
            ol(['buttercups and cuckooflower in the wettest corners,', 'oxeye daisies along the drier top edge,', 'knapweed and bird&rsquo;s-foot trefoil by midsummer,', 'and the seed heads, which the goldfinches strip in August.']),
            p('None of it would happen if the field were cut in May. The farmer here mows in late July, after most of the flowers have set seed, and the meadow is richer every year for it.'),
            quote('A late mow is the cheapest conservation there is.', 'Our neighbour, leaning on the gate'),
        ), excerpt='One week in April, the lower field turns from green to gold. Notes on buttercups, and on the flowers that follow them.',
            comments=[comment('Hannah', 'We have the same field behind our house and I never knew why it shone like that. Thank you!', '2026-04-19T10:12:00'),
                      comment('Tomas', 'Lovely post. Our council has started cutting the verges later too, and the difference in two years is remarkable.', '2026-04-20T18:40:00')], sticky=True),
        post('reading-a-creek-in-five-minutes', 'Reading a creek in five minutes', '2026-05-09T09:00:00', ['waterways', 'field-guides'], ['conservation'], 'creek', blocks(
            p('You do not need a kit or a degree to get a sense of how healthy a small creek is. Five quiet minutes on the bank tell you a lot, if you know where to look.'),
            h('Look at the stones'),
            p('Turn one over, gently, and put it back the way it was. Caddisfly cases, small cases built from sand grains or bits of leaf, are a very good sign: their larvae need clean, well-oxygenated water.'),
            h('Look at the edges'),
            p('Overhanging plants keep the water cool and drop insects for fish. A bank of bare mud with nothing growing on it usually means the water level swings hard, or that cattle reach the water.'),
            h('Look at the flow'),
            p('A healthy creek is rarely uniform. Fast riffles over gravel, slow pools on the outside of a bend, a fallen branch making an eddy: every change in speed makes a different home.'),
            img('creek', 'A shallow creek over stones', 'Riffles over gravel: shallow, fast and full of oxygen.'),
            p('None of this replaces proper monitoring, but it turns a walk along the water into something more: a conversation with the place.'),
        ), comments=[comment('Priya', 'Took my kids to our local stream with this list. We found three caddis cases and one very surprised crayfish.', '2026-05-11T16:05:00')]),
        post('a-dawn-count-at-the-marsh', 'A dawn count at the marsh', '2026-05-24T06:15:00', ['waterways'], ['birds', 'conservation'], 'birds', blocks(
            p('The volunteers meet in the car park at five, with flasks and fingerless gloves. The count starts when there is just enough light to see the far reeds.'),
            p('We work in pairs: one counts, one writes. It is harder than it sounds. A flock of waders lifts, turns and lands again twenty metres away, and you have to decide whether those are the same birds or new ones.'),
            quote('Count what you see, not what you expect to see.', 'The first rule of the morning'),
            p('By seven the light is flat and the birds are feeding, heads down, and the count settles. This spring the totals were steady for most species and slightly up for the small waders, which the refuge staff put down to the new shallow scrapes dug last autumn.'),
            p('If you live near a reserve, ask whether they run counts. Nobody minds a beginner who is happy to hold the clipboard.'),
        )),
        post('walking-the-cliff-dwellings-at-first-light', 'Walking the cliff dwellings at first light', '2026-06-14T07:00:00', ['trails'], ['slow-travel', 'photography'], 'cliff', blocks(
            p('Most visitors arrive at midday, when the sandstone is white with glare. We went at opening time, when the alcove is still in shade and the stone walls glow a soft orange from the light bouncing off the canyon floor.'),
            img('cliff', 'Stone dwellings under a cliff overhang', 'Built into the alcove more than seven hundred years ago.'),
            h('Practical notes'),
            ul(['Tours to the dwellings themselves are guided and limited; book ahead.', 'The ladder climbs are short but exposed. Go at your own pace.', 'Bring more water than you think. The mesa is high and dry.']),
            p('The best part was not the photographs. It was standing still in the doorway of a room people lived in for generations, and hearing nothing but swifts.'),
        )),
        post('how-to-press-flowers-without-picking-the-last-one', 'How to press flowers without picking the last one', '2026-06-28T11:00:00', ['field-guides', 'meadows'], ['wildflowers', 'conservation'], 'meadow-square', blocks(
            p('Pressing flowers is a lovely way to remember a walk. Done carelessly, it is also a way to make a rare plant rarer. A few simple rules keep it on the right side.'),
            ol(['Pick from your own garden, or from plants that are truly abundant where you are.', 'Never pick the only one, and never more than one in twenty.', 'Leave protected species, and anything on a reserve, entirely alone.', 'Take a photograph instead when in doubt. It lasts longer anyway.']),
            h('Pressing'),
            p('Lay the flowers between sheets of plain paper inside a heavy book, add more books on top, and leave them for two weeks. Change the paper after two or three days if the flowers are fleshy.'),
            p('Thin petals, daisies, violets, buttercups, press beautifully. Thick, round flowers are better dried hanging upside down.'),
        )),
        post('one-tree-on-the-ridge', 'One tree on the ridge', '2026-08-02T18:30:00', ['trails'], ['photography'], 'ridge', blocks(
            p('There is a single small tree on the ridge above the harbour, and we have photographed it in every month of the year. It is not a remarkable tree. That is rather the point.'),
            p('A familiar subject teaches you to see light. In winter the tree is a dark mark against a white sky; in late summer, at the end of the day, it stands in long gold grass with its shadow running halfway down the slope.'),
            quote('Go back to the same place. It will never be the same place twice.'),
            p('If you want to try this, choose something you pass often: a tree, a gate, a bend in a river. Stand in the same spot each time. After a year, lay the pictures side by side.'),
        )),
        post('below-the-surface-a-morning-at-the-reef', 'Below the surface: a morning at the reef', '2026-09-06T09:45:00', ['waterways'], ['slow-travel'], 'coral', blocks(
            p('We spent a morning snorkelling over a shallow reef in a state park, a few miles offshore. The water was so clear that the boat&rsquo;s shadow lay on the sand three metres down.'),
            p('Reefs are fragile, and the rules are simple: no touching, no standing, reef-safe sun cream or none at all, and a good buoyancy vest so you can hang still above the coral instead of kicking it.'),
            img('coral', 'Coral in clear water'),
            p('What surprised us most was the noise. Put your head under and the reef crackles: shrimps, fish grazing on the coral, the whole place busy and alive.'),
        )),
    ],
    'menus': [
        {'name': 'Main menu', 'location': 'primary', 'items': [
            {'title': 'Home', 'page': 'home'}, {'title': 'Journal', 'page': 'journal'},
            {'title': 'Topics', 'url': '#', 'children': [{'category': 'meadows'}, {'category': 'waterways'}, {'category': 'trails'}, {'category': 'field-guides'}]},
            {'page': 'about'}, {'page': 'contact'}]},
        {'name': 'Footer menu', 'location': 'footer', 'items': [{'page': 'about'}, {'page': 'journal'}, {'page': 'contact'}]},
    ],
})

# 2. Botanica: a botanical garden studio and florist ----------------------------------------------------

SETS.append({
    'slug': 'botanica',
    'name': 'Botanica',
    'description': 'A garden studio and flower shop: seasonal flowers, plant care notes, workshops and opening hours.',
    'preview': 'hibiscus-wide',
    'options': {'blogname': 'Botanica', 'blogdescription': 'Garden studio and flower shop', 'posts_per_page': 6},
    'customizer': {'color_scheme': 'afternoon'},
    'media': [
        media('hibiscus-wide', 'grid-flower-1.webp', 'Red hibiscus flowers against dark green leaves'),
        media('botany', 'botany-flowers.webp', 'A bunch of garden flowers in soft colors', 1400),
        media('botany-close', 'botany-flowers-closeup.webp', 'Close view of garden flowers', 1200),
        media('hibiscus', 'red-hibiscus-closeup.webp', 'A single red hibiscus flower, close up', 1079),
        media('delphinium', 'delphinium-flowers.webp', 'Botanical illustration of blue delphinium flowers', 577),
        media('campanula', 'campanula-alliariifolia-flower.webp', 'Botanical illustration of Cornish bellflower', 577),
        media('thistle', 'star-thristle-flower.webp', 'Botanical illustration of a star thistle', 577),
        media('anthurium', 'category-anthuriums.webp', 'Anthuriums growing under a sun screen in a greenhouse', 297),
        media('sunflower', 'category-sunflowers.webp', 'A tall sunflower in bloom', 297),
        media('cactus', 'category-cactus.webp', 'Saguaro cactus arms against the sky', 297),
        media('phacelia', 'grid-flower-2.webp', 'Lacy phacelia, enlarged, from a 1928 plant photograph', 794),
    ],
    'categories': [
        {'slug': 'seasonal-flowers', 'name': 'Seasonal Flowers', 'description': 'What is in the shop and in the beds this month.'},
        {'slug': 'plant-care', 'name': 'Plant Care', 'description': 'Light, water and patience, plant by plant.'},
        {'slug': 'garden-diary', 'name': 'Garden Diary', 'description': 'Notes from the cutting garden behind the shop.'},
        {'slug': 'workshops', 'name': 'Workshops', 'description': 'Hands-on sessions in the studio.'},
    ],
    'tags': [{'slug': s, 'name': n} for s, n in [('cut-flowers', 'Cut flowers'), ('houseplants', 'Houseplants'), ('pollinators', 'Pollinators'),
                                                   ('shade', 'Shade'), ('summer', 'Summer'), ('drought', 'Drought')]],
    'pages': [
        {'slug': 'home', 'title': 'Home', 'front_page': True, 'template': 'page-no-title', 'content': blocks(
            cover('hibiscus-wide', [
                p('Garden studio &amp; flower shop', size='medium'),
                h('Flowers grown, cut and arranged by hand', 1, size='xx-large'),
                p('Seasonal bouquets from our own cutting garden, plants that will outlive their pot, and Saturday workshops at the long table.', size='large'),
                buttons([('Visit the shop', '{{page:visit}}', False), ('See the workshops', '{{page:workshops}}', True)]),
            ], height=600, dim=40),
            group([
                h('In season now', align='center'),
                p('Everything in the shop comes from within a short drive, most of it from the beds you can see from the counter. When a flower is over, it is over until next year, and we think that is part of its charm.', align='center', size='medium'),
            ]),
            columns([
                card('delphinium', 'Delphinium illustration', 'Delphiniums', 'The tall blues that stop people in the street, and how to keep them standing.', '{{post:delphiniums-the-blue-everyone-asks-about}}', 'Growing notes &rarr;'),
                card('campanula', 'Bellflower illustration', 'Bellflowers', 'Our favourite answer to a shady corner that nothing else will grow in.', '{{post:bellflowers-for-shady-corners}}', 'Growing notes &rarr;'),
                card('thistle', 'Star thistle illustration', 'Thistles', 'Why a few thistles stay in the garden, prickles and all.', '{{post:why-we-leave-the-thistles}}', 'Read why &rarr;'),
            ]),
            group([
                columns([
                    [img('botany', 'A bunch of garden flowers')],
                    [h('Saturday workshops'), p('Two hours, a table full of flowers, and a bouquet to take home. Small groups, all materials included, tea and cake at half time.'),
                     buttons([('Book a place', '{{page:workshops}}', False)])],
                ]),
            ], align='full', section='section-1'),
            group([
                h('From the garden diary'),
                latest_posts(3),
            ]),
        )},
        {'slug': 'journal', 'title': 'Garden Diary', 'posts_page': True, 'content': ''},
        {'slug': 'about', 'title': 'Our Studio', 'featured_media': 'botany-close', 'content': blocks(
            p('Botanica is a small studio at the end of a long garden. We grow most of what we sell in raised beds and two old greenhouses, and arrange it at a scrubbed oak table that has seen more flowers than we can count.', size='large'),
            p('We started with a market stall and a van. The stall became a shop, the shop grew a garden, and the garden slowly took over the car park. Customers tell us they come for the flowers and stay for the conversation, which suits us fine.'),
            h('What we believe'),
            ul(['Flowers should be in season, local and free of plastic wrap.', 'A garden for cutting can still be a garden for bees.', 'Anyone can learn to arrange flowers; it just takes a few good ones and permission to experiment.']),
        )},
        {'slug': 'workshops', 'title': 'Workshops', 'featured_media': 'phacelia', 'content': blocks(
            p('Our workshops run on Saturday mornings from spring to late autumn. Each one is two hours long, limited to eight people, and includes every flower, tool and slice of cake.', size='large'),
            h('This season'),
            ul(['<strong>Hand-tied bouquets</strong>: the spiral technique, step by step.', '<strong>Table arrangements</strong>: low, loose and long-lasting, without floral foam.', '<strong>Dried flower wreaths</strong>: for autumn and well into winter.', '<strong>Houseplant clinic</strong>: bring a sad plant, take home a plan.']),
            p('To book, call in at the shop or write to <a href="mailto:studio@example.com">studio@example.com</a>. Gift vouchers are available for every workshop.'),
            buttons([('Plan your visit', '{{page:visit}}', False)]),
        )},
        {'slug': 'visit', 'title': 'Visit', 'content': blocks(
            p('The shop and garden are open to everyone. Wander round the beds, ask us anything, and leave with something that smells wonderful.', size='large'),
            columns([
                [h('Opening hours', 3), p('Tuesday to Friday, 9 am to 5 pm<br>Saturday, 9 am to 4 pm<br>Closed Sunday and Monday')],
                [h('Find us', 3), p('12 Orchard Lane<br>At the end of the gravel drive, past the greenhouses'), p('<a href="mailto:studio@example.com">studio@example.com</a>')],
            ], align=None),
            p('Weddings and events: we are happy to talk about seasonal flowers for your day. Please get in touch at least three months ahead.'),
        )},
    ],
    'posts': [
        post('delphiniums-the-blue-everyone-asks-about', 'Delphiniums: the blue everyone asks about', '2026-06-06T10:00:00', ['seasonal-flowers', 'plant-care'], ['cut-flowers', 'summer'], 'delphinium', blocks(
            p('Every June someone stops at the gate and asks what the tall blue flowers are. They are delphiniums, and they are worth the small amount of fuss they need.'),
            img('delphinium', 'Blue delphinium flowers, botanical illustration', 'Delphinium, as drawn for an old botanical plate.'),
            h('Growing them'),
            ul(['Full sun and rich, well-drained soil.', 'Stake them early, before the first summer storm, not after.', 'Slugs love young shoots; protect them in spring.', 'Cut the first spikes as they fade and you will often get a second, shorter flush in late summer.']),
            h('In the vase'),
            p('Cut when about a third of the flowers on the spike are open, and put them straight into deep, cool water. They drop their petals when they are done, all at once, which is either sad or rather beautiful depending on your mood.'),
        ), comments=[comment('Margaret', 'Mine flopped over in the first rain last year. Staking this year, early, as instructed!', '2026-06-07T09:20:00')]),
        post('bellflowers-for-shady-corners', 'Bellflowers for shady corners', '2026-05-16T10:00:00', ['plant-care', 'garden-diary'], ['shade', 'pollinators'], 'campanula', blocks(
            p('Every garden has one: the strip along the north fence where nothing seems to grow. Ours used to be bare earth and a hose reel. Now it is full of bellflowers.'),
            p('Campanulas cope with part shade better than most flowering perennials, and the Cornish bellflower in particular will spread into a loose, white-flowered carpet that bees adore.'),
            quote('Plant for the place you have, not the place you wish you had.'),
            p('Give them a mulch of leaf mould in autumn, cut them back after the first flowering, and they will ask very little else.'),
        )),
        post('why-we-leave-the-thistles', 'Why we leave the thistles', '2026-07-11T10:00:00', ['garden-diary'], ['pollinators'], 'thistle', blocks(
            p('Visitors are sometimes surprised to find thistles in a flower garden. We leave a few on purpose.'),
            p('On a warm afternoon a single thistle head can have three or four kinds of bee on it at once. Later the seed heads feed goldfinches, and the down lines more than one nest.'),
            p('We keep them to one corner, cut most heads before they seed, and wear gloves. That is all it takes to share the garden with the insects that pollinate everything else in it.'),
        )),
        post('anthuriums-indoors-light-water-patience', 'Anthuriums indoors: light, water, patience', '2026-02-21T10:00:00', ['plant-care'], ['houseplants'], 'anthurium', blocks(
            p('Anthuriums are the glossy, heart-shaped flowers that last for weeks. In the wild they grow in the warm, damp shade of the forest, and indoors they want something similar.'),
            ol(['<strong>Light</strong>: bright but indirect. A metre from a sunny window is ideal.', '<strong>Water</strong>: when the top few centimetres of compost are dry, not before.', '<strong>Humidity</strong>: a bathroom with a window is anthurium heaven.', '<strong>Patience</strong>: a new plant often sulks for a month while it settles in.']),
            p('If the leaves yellow, it is usually too much water. If the flowers stop, it is usually too little light.'),
        )),
        post('the-sunflower-wall-week-by-week', 'The sunflower wall, week by week', '2026-08-15T10:00:00', ['garden-diary', 'seasonal-flowers'], ['summer', 'cut-flowers'], 'sunflower', blocks(
            p('In April we sowed a single row of sunflowers along the back wall. By August they were taller than the wall.'),
            ul(['<strong>Week 1</strong>: seed in, netting over to keep the birds off.', '<strong>Week 4</strong>: thinned to one plant every 30 centimetres.', '<strong>Week 10</strong>: first buds, and the first visitors asking about them.', '<strong>Week 16</strong>: in full flower, facing east every morning.']),
            p('For cutting, choose the branching varieties: one plant gives a dozen smaller flowers over the summer instead of a single giant head.'),
        )),
        post('desert-lessons-from-a-single-saguaro', 'Desert lessons from a single saguaro', '2026-03-14T10:00:00', ['plant-care'], ['drought', 'houseplants'], 'cactus', blocks(
            p('A saguaro may take ten years to grow its first few centimetres and seventy before it grows an arm. It is a good plant to think about when you are tempted to water a cactus on the windowsill every week.'),
            p('Desert plants store water and grow slowly. Indoors that means a gritty compost, a pot with a drainage hole, the sunniest spot you have, and water only when the compost is completely dry, which in winter can mean once a month.'),
            quote('Most cacti that die indoors are loved to death.'),
        )),
        post('hibiscus-that-last-more-than-a-day', 'Hibiscus that last more than a day', '2026-07-25T10:00:00', ['seasonal-flowers'], ['summer', 'houseplants'], 'hibiscus', blocks(
            p('Each hibiscus flower opens for a day or two and then it is done. The trick to a plant that is always in flower is not a longer-lasting bloom, it is more buds.'),
            img('hibiscus', 'A red hibiscus flower'),
            p('Feed every two weeks in summer with a high-potash feed, keep the plant in the brightest spot you have, and do not move it once it has buds: hibiscus drops them at the slightest change.'),
        )),
    ],
    'menus': [
        {'name': 'Main menu', 'location': 'primary', 'items': [
            {'title': 'Home', 'page': 'home'}, {'page': 'about'}, {'page': 'workshops'}, {'page': 'journal'}, {'page': 'visit'}]},
        {'name': 'Footer menu', 'location': 'footer', 'items': [{'category': 'seasonal-flowers'}, {'category': 'plant-care'}, {'page': 'visit'}]},
    ],
})

# 3. Field & Light: a landscape photography portfolio --------------------------------------------------

SETS.append({
    'slug': 'field-and-light',
    'name': 'Field & Light',
    'description': 'A landscape photography portfolio: a gallery homepage, prints and commissions, and a journal of stories behind the pictures.',
    'preview': 'meadow',
    'options': {'blogname': 'Field & Light', 'blogdescription': 'Landscape photography', 'posts_per_page': 6},
    'customizer': {'color_scheme': 'twilight'},
    'media': [
        media('meadow', 'coming-soon-bg-image.webp', 'Wildflower meadow stretching to the horizon'),
        media('buttercups', 'northern-buttercups-flowers.webp', 'Northern buttercups in a field'),
        media('ridge', '404-image.webp', 'A small tree on a grassy ridge above a harbour', 1048),
        media('cliff', 'poster-image-background.webp', 'Cliff dwellings beneath a sandstone overhang', 1200),
        media('ruins', 'ruins-image.webp', 'Stone towers of an ancient cliff village', 678),
        media('creek', 'dallas-creek-square.webp', 'A creek winding through grassland', 594),
        media('birds', 'marshland-birds-square.webp', 'Birds in a marsh at the edge of reeds', 594),
        media('coral', 'coral-square.webp', 'Coral under clear shallow water', 594),
        media('meadow-wide', 'services-subscriber-photo.webp', 'Flowers in a meadow, close to the ground', 1058),
    ],
    'categories': [
        {'slug': 'landscapes', 'name': 'Landscapes', 'description': 'Wide views and the light that makes them.'},
        {'slug': 'stories', 'name': 'Stories', 'description': 'The walks and waits behind the pictures.'},
        {'slug': 'technique', 'name': 'Technique', 'description': 'Notes on gear, light and composition.'},
    ],
    'tags': [{'slug': s, 'name': n} for s, n in [('golden-hour', 'Golden hour'), ('black-and-white', 'Black and white'), ('long-lens', 'Long lens'),
                                                   ('filters', 'Filters'), ('seasons', 'Seasons')]],
    'pages': [
        {'slug': 'home', 'title': 'Home', 'front_page': True, 'template': 'page-no-title', 'content': blocks(
            cover('meadow', [
                h('Patient pictures of quiet places', 1, size='xx-large'),
                p('Landscape photography by Mara Ellison: meadows, ridges, marshes and the long minutes of waiting for the light.', size='large'),
                buttons([('View the portfolio', '{{page:portfolio}}', False), ('Prints &amp; commissions', '{{page:prints}}', True)]),
            ], height=680, dim=40),
            group([
                gallery([('ridge', 'A tree on a ridge'), ('cliff', 'Cliff dwellings'), ('buttercups', 'Buttercups'),
                         ('creek', 'A creek'), ('birds', 'Marsh birds'), ('coral', 'Coral')], 3),
            ], align='wide'),
            group([
                columns([
                    [h('About the work'), p('I photograph a small number of places many times, in every season and every kind of weather. The pictures here are the ones where the place and the light finally agreed.')],
                    [h('Prints'), p('Every photograph is available as a signed archival print in three sizes, printed on cotton rag paper and shipped flat.'), buttons([('Order a print', '{{page:prints}}', True)])],
                ]),
            ]),
            group([
                h('From the journal'),
                latest_posts(3),
            ]),
        )},
        {'slug': 'portfolio', 'title': 'Portfolio', 'template': 'page-no-title', 'content': blocks(
            group([
                h('Portfolio', 1),
                p('Selected photographs, 2022 to 2026. Click through the journal for the stories behind them.', size='large'),
            ]),
            gallery([('meadow-wide', 'Flowers close to the ground'), ('ridge', 'A tree on a ridge'), ('ruins', 'Stone towers'),
                     ('cliff', 'Cliff dwellings'), ('buttercups', 'Buttercups'), ('meadow', 'A wide meadow'),
                     ('creek', 'A creek'), ('birds', 'Marsh birds'), ('coral', 'Coral')], 3),
            spacer('60'),
        )},
        {'slug': 'journal', 'title': 'Journal', 'posts_page': True, 'content': ''},
        {'slug': 'about', 'title': 'About', 'featured_media': 'ridge', 'content': blocks(
            p('I am a landscape photographer working mostly within a day&rsquo;s walk of home. I came to photography late, after years of walking the same hills with a notebook, and it changed how I look at everything.', size='large'),
            p('My approach is slow. I visit a place, often without a camera, and come back when the season, the weather and the time of day line up with the picture I have in mind. Sometimes that takes a week. Sometimes it takes three years.'),
            h('Exhibitions and publications'),
            ul(['<em>Edges</em>, a group show of coastal photography, Harbour Gallery, 2025', '<em>Twelve Months on the Ridge</em>, a small book of photographs, 2024', 'Regular contributor to regional walking and nature magazines']),
        )},
        {'slug': 'prints', 'title': 'Prints & Commissions', 'featured_media': 'buttercups', 'content': blocks(
            p('All photographs on this site are available as signed, limited-edition prints.', size='large'),
            ul(['<strong>Small</strong>: 30 &times; 40 cm, edition of 50', '<strong>Medium</strong>: 50 &times; 70 cm, edition of 25', '<strong>Large</strong>: 70 &times; 100 cm, edition of 10']),
            p('Prints are made on 100% cotton rag paper with archival pigment inks and are shipped flat, unframed, with a certificate of authenticity.'),
            h('Commissions'),
            p('I take on a small number of commissions each year, mostly for books, landscape architects and conservation charities. Tell me about the place and what you need the pictures to do.'),
            buttons([('Get in touch', '{{page:contact}}', False)]),
        )},
        {'slug': 'contact', 'title': 'Contact', 'content': blocks(
            p('For prints, commissions, licensing and talks, write to <a href="mailto:studio@example.com">studio@example.com</a>. I reply within a few days, unless I am somewhere without a signal, which is more often than you might think.', size='large'),
        )},
    ],
    'posts': [
        post('waiting-for-the-wind-to-stop', 'Waiting for the wind to stop', '2026-05-30T20:00:00', ['stories'], ['golden-hour', 'seasons'], 'meadow', blocks(
            p('The meadow photograph on the front page took four evenings. The light was right on all four. The wind was not.'),
            p('Flowers move. At a shutter speed slow enough for a small aperture and low ISO, every stem becomes a soft streak. Some photographers love that; I wanted every petal still.'),
            p('On the fourth evening the breeze dropped about ten minutes before sunset, as it often does inland. I had perhaps ninety seconds of stillness. I made eleven frames. One of them is the picture.'),
            quote('Landscape photography is mostly waiting, with a little bit of pressing a button at the end.'),
        ), comments=[comment('Jonas', 'Ninety seconds after four evenings. That is the most honest description of landscape work I have read.', '2026-06-01T08:30:00')]),
        post('the-ridge-three-seasons', 'The ridge, three seasons', '2026-03-08T17:00:00', ['landscapes', 'stories'], ['seasons'], 'ridge', blocks(
            p('A single tree on a ridge, photographed from the same spot in winter, spring and late summer.'),
            img('ridge', 'A tree on a grassy ridge'),
            p('Revisiting a subject is the best exercise I know. The first visit is about the place; every visit after is about the light, the season and what you have learned since.'),
        )),
        post('shooting-into-the-light-at-the-cliff-dwellings', 'Shooting into the light at the cliff dwellings', '2026-04-12T07:30:00', ['technique', 'landscapes'], ['golden-hour'], 'cliff', blocks(
            p('Midday at the cliff dwellings is all contrast: blinding sandstone, black shadow. At opening time, the alcove is in shade and lit by light reflected off the canyon, which is soft, warm and forgiving.'),
            h('What helped'),
            ul(['Arriving before the sun reached the alcove.', 'Bracketing three exposures for the brightest sky.', 'A lens hood, and a hand held up to shade the front element.']),
            img('ruins', 'Stone towers of the cliff village'),
        )),
        post('why-i-still-carry-a-polarizer', 'Why I still carry a polarizer', '2026-02-14T12:00:00', ['technique'], ['filters'], 'coral', blocks(
            p('Most effects can be added later on a computer. A polarizing filter is one of the few that cannot: it removes reflections at the moment of capture, and no software can recover what the reflection hid.'),
            p('On water it lets you see the stones in a creek bed. On wet leaves after rain it brings back their color. On a blue sky it deepens the blue, though a wide lens will show it unevenly, so go gently.'),
            quote('Turn the ring slowly and watch. When the glare disappears, stop.'),
        )),
        post('marsh-birds-and-the-long-lens', 'Marsh birds and the long lens', '2026-09-20T06:30:00', ['technique', 'stories'], ['long-lens'], 'birds', blocks(
            p('I am not a wildlife photographer, but a marsh at dawn is a landscape full of birds, and sometimes you want them in the frame.'),
            p('A long lens compresses distance and lets you stay well back, which matters more than any technique. If a bird changes what it is doing because of you, you are too close.'),
            p('Sit still, let the birds forget you, and photograph the scene rather than the bird: reeds, water, light, and somewhere in it, a small shape feeding.'),
        )),
        post('a-creek-in-black-and-white', 'A creek in black and white', '2026-10-04T15:00:00', ['landscapes'], ['black-and-white'], 'creek', blocks(
            p('Some days the color is the subject. Other days it gets in the way. On a grey afternoon the creek was all texture: wet stone, moving water, dark grass.'),
            p('Converting to black and white is not just removing color; it is deciding how bright each color becomes. Darkening the greens made the water glow, and the picture finally said what I saw.'),
        )),
    ],
    'menus': [
        {'name': 'Main menu', 'location': 'primary', 'items': [
            {'page': 'portfolio'}, {'page': 'journal'}, {'page': 'prints'}, {'page': 'about'}, {'page': 'contact'}]},
        {'name': 'Footer menu', 'location': 'footer', 'items': [{'category': 'landscapes'}, {'category': 'stories'}, {'category': 'technique'}]},
    ],
})

# 4. Slow Seasons: a slow-living magazine ---------------------------------------------------------------

SETS.append({
    'slug': 'slow-seasons',
    'name': 'Slow Seasons',
    'description': 'A slow-living magazine: home and garden, seasonal cooking, time outdoors and short essays, with a newsletter page.',
    'preview': 'botany-close',
    'options': {'blogname': 'Slow Seasons', 'blogdescription': 'A magazine for unhurried days', 'posts_per_page': 6},
    'customizer': {'color_scheme': 'morning'},
    'media': [
        media('botany-close', 'botany-flowers-closeup.webp', 'Garden flowers gathered in soft light', 1200),
        media('meadow-square', 'flower-meadow-square.webp', 'Wildflowers in a summer meadow', 594),
        media('hibiscus', 'red-hibiscus-closeup.webp', 'A red hibiscus flower', 1079),
        media('phacelia', 'grid-flower-2.webp', 'Lacy phacelia flower photographed close up', 794),
        media('sunflower', 'category-sunflowers.webp', 'A sunflower in late summer', 297),
        media('creek', 'dallas-creek-square.webp', 'A creek through grassland', 594),
        media('plant', 'malibu-plantlife.webp', 'Coastal plants in sunlight', 364),
        media('campanula', 'campanula-alliariifolia-flower.webp', 'A bellflower drawn in an old botanical plate', 577),
        media('meadow-wide', 'services-subscriber-photo.webp', 'A meadow seen from the grass', 1058),
    ],
    'categories': [
        {'slug': 'home-and-garden', 'name': 'Home & Garden', 'description': 'Small changes that make a home feel like one.'},
        {'slug': 'kitchen', 'name': 'Kitchen', 'description': 'Cooking with the season, without fuss.'},
        {'slug': 'outdoors', 'name': 'Outdoors', 'description': 'Time outside, close to home.'},
        {'slug': 'essays', 'name': 'Essays', 'description': 'Short pieces on living at a gentler pace.'},
    ],
    'tags': [{'slug': s, 'name': n} for s, n in [('herbs', 'Herbs'), ('rituals', 'Rituals'), ('walking', 'Walking'), ('autumn', 'Autumn'),
                                                   ('summer', 'Summer'), ('letters', 'Letters')]],
    'pages': [
        {'slug': 'home', 'title': 'Home', 'front_page': True, 'template': 'page-no-title', 'content': blocks(
            group([
                columns([
                    [p('Issue twelve &middot; Late summer', size='small'),
                     h('Unhurried days, well spent', 1, size='xx-large'),
                     p('Slow Seasons is a magazine about the small, good things: herbs on the windowsill, a walk before breakfast, a meal made from whatever the garden gave you today.', size='large'),
                     buttons([('Read the latest', '{{page:stories}}', False), ('Get the newsletter', '{{page:newsletter}}', True)])],
                    [img('botany-close', 'Garden flowers gathered in soft light')],
                ]),
            ], align='wide', pad='50'),
            group([
                h('This season', align='center'),
                columns([
                    card('phacelia', 'Phacelia flower', 'A windowsill herb garden', 'Four pots, one sunny ledge and fresh herbs all year.', '{{post:a-windowsill-herb-garden-in-four-pots}}', 'Read &rarr;'),
                    card('sunflower', 'Sunflower', 'Cooking with August', 'What to make when the garden gives you everything at once.', '{{post:cooking-with-what-the-garden-gives-you-in-august}}', 'Read &rarr;'),
                    card('creek', 'A creek', 'The same path, every day', 'A month-long walking experiment, and what it taught us.', '{{post:walk-the-same-path-every-day-for-a-month}}', 'Read &rarr;'),
                ]),
            ], align='wide'),
            group([
                quote('Nature does not hurry, yet everything is accomplished.', 'Lao Tzu'),
            ], align='full', section='section-1'),
            group([
                h('Recent stories'),
                latest_posts(4),
            ]),
        )},
        {'slug': 'stories', 'title': 'Stories', 'posts_page': True, 'content': ''},
        {'slug': 'about', 'title': 'About', 'featured_media': 'meadow-wide', 'content': blocks(
            p('Slow Seasons is written by a small group of friends who share a belief that a good life is mostly made of ordinary days, and that ordinary days deserve a little care.', size='large'),
            p('We write about homes, gardens, kitchens and the outdoors, always close to where we live, and always in season. There are no affiliate links here and nothing to buy. Just ideas to borrow.'),
            h('Write for us'),
            p('We publish occasional essays from readers. If you have a short piece about slowing down, we would love to read it: send it to <a href="mailto:editors@example.com">editors@example.com</a>.'),
        )},
        {'slug': 'newsletter', 'title': 'Newsletter', 'content': blocks(
            p('Once a month, on the first Sunday, we send a short letter: one essay, one recipe, one thing to notice outside this month. No adverts, ever.', size='large'),
            p('To subscribe, send an empty message to <a href="mailto:letters@example.com">letters@example.com</a> with &ldquo;subscribe&rdquo; in the subject. You can leave just as easily, whenever you like.'),
            img('campanula', 'A bellflower drawn in an old botanical plate', 'Every letter comes with a plate from an old botanical book.'),
        )},
    ],
    'posts': [
        post('a-windowsill-herb-garden-in-four-pots', 'A windowsill herb garden in four pots', '2026-04-05T09:00:00', ['home-and-garden', 'kitchen'], ['herbs'], 'phacelia', blocks(
            p('You do not need a garden to grow herbs, just a bright windowsill and four pots. These four give the most flavour for the least trouble.'),
            ol(['<strong>Chives</strong>: snip often and they keep coming. Their purple flowers are edible too.', '<strong>Thyme</strong>: likes it dry. Water sparingly and give it the sunniest spot.', '<strong>Mint</strong>: always in its own pot, or it takes over. Keep it moist.', '<strong>Parsley</strong>: slow to start, generous once it gets going.']),
            p('Turn the pots every week so they grow evenly, and pick from the top to keep them bushy. A few leaves a day changes how a kitchen smells.'),
        ), comments=[comment('Elif', 'Started with chives and mint after reading this. Two months in, I cannot imagine cooking without them.', '2026-06-02T19:10:00')]),
        post('the-case-for-an-unscheduled-sunday', 'The case for an unscheduled Sunday', '2026-03-22T08:00:00', ['essays'], ['rituals'], 'meadow-square', blocks(
            p('For a year we kept one day a week free of plans. Not free of activity: we still cooked, walked, read and met friends. Free of plans.'),
            p('The difference was in the deciding. On a planned day, you move from one thing to the next. On an unplanned day, you notice that the light is good and go out in it, or that the soup needs another hour and give it one.'),
            quote('A day with nothing in it fills itself with exactly the right things.'),
            p('It does not have to be Sunday, or a whole day. Try one afternoon a week with nothing written down, and see what grows in the space.'),
        )),
        post('cooking-with-what-the-garden-gives-you-in-august', 'Cooking with what the garden gives you in August', '2026-08-23T12:00:00', ['kitchen'], ['summer'], 'sunflower', blocks(
            p('August in the garden is generous to the point of rudeness. Courgettes appear overnight. The beans need picking twice a day. Everyone has tomatoes.'),
            h('Three things we make all month'),
            ul(['<strong>A tray of roasted everything</strong>: tomatoes, courgettes, onions and garlic, olive oil, a hot oven, an hour. Eat it warm, cold, on toast or blended into soup.', '<strong>Quick pickles</strong>: thin slices of cucumber or courgette in vinegar, sugar and salt, ready in an hour.', '<strong>Green sauce</strong>: every soft herb in the garden, chopped with capers, lemon and oil.']),
            p('None of these need a recipe, only a willingness to use what is there.'),
        )),
        post('learning-the-names-of-ten-plants-on-your-street', 'Learning the names of ten plants on your street', '2026-05-10T10:00:00', ['outdoors', 'essays'], ['walking'], 'plant', blocks(
            p('Most of us walk past hundreds of plants a day without knowing a single name. Learning just ten changes the walk completely.'),
            p('Start with what is common: the tree outside your door, the weed between the paving stones, the shrub in the neighbour&rsquo;s hedge. Take a photograph, look it up, say the name out loud the next time you pass.'),
            p('After ten, you start noticing the eleventh without trying. The street becomes a place of acquaintances, which is a small, real kind of happiness.'),
        )),
        post('walk-the-same-path-every-day-for-a-month', 'Walk the same path every day for a month', '2026-06-20T07:00:00', ['outdoors'], ['walking', 'rituals'], 'creek', blocks(
            p('In June, one of us walked the same twenty-minute path along a creek every morning. Same route, same time, no headphones.'),
            p('The first week was boring. The second week, things began to stand out: a heron that was always on the same stone, a patch of flowers opening a little more each day. By the fourth week the walk felt less like exercise and more like reading a story in daily instalments.'),
            quote('You cannot step into the same river twice, but you can walk beside it every day and watch it change.'),
        )),
        post('drying-flowers-for-winter', 'Drying flowers for winter', '2026-09-27T10:00:00', ['home-and-garden'], ['autumn'], 'botany-close', blocks(
            p('Late summer is the time to cut flowers for drying, while they are at their best and before the autumn damp arrives.'),
            ol(['Cut on a dry day, after the morning dew has gone.', 'Strip the lower leaves and tie the stems in small bunches.', 'Hang them upside down somewhere dark, dry and airy.', 'Leave them for two to three weeks, until the stems snap cleanly.']),
            p('Statice, strawflowers, lavender, grasses and seed heads all dry well. A jar of them on the table in January is a small promise that summer will come back.'),
        )),
        post('letters-not-notifications', 'Letters, not notifications', '2026-10-11T09:00:00', ['essays'], ['letters', 'rituals'], 'hibiscus', blocks(
            p('A notification asks for your attention now. A letter waits until you are ready.'),
            p('This autumn we started writing to friends again, on paper, once a month. The letters are short and nothing much happens in them: what is flowering, what we cooked, a book we liked. The replies arrive weeks later, and they are the best post we get.'),
            p('If you try it, do not aim for eloquence. Aim for regular. A postcard every month is worth more than a beautiful letter you never send.'),
        )),
    ],
    'menus': [
        {'name': 'Main menu', 'location': 'primary', 'items': [
            {'title': 'Home', 'page': 'home'}, {'page': 'stories'},
            {'title': 'Sections', 'url': '#', 'children': [{'category': 'home-and-garden'}, {'category': 'kitchen'}, {'category': 'outdoors'}, {'category': 'essays'}]},
            {'page': 'about'}, {'page': 'newsletter'}]},
        {'name': 'Footer menu', 'location': 'footer', 'items': [{'page': 'about'}, {'page': 'newsletter'}]},
    ],
})


def magick(*args):
    subprocess.run(['magick'] + list(args), check=True)


def build(d):
    out = os.path.join(OUT, 'demos', d['slug'], 'demo')
    os.makedirs(os.path.join(out, 'images'), exist_ok=True)
    medias = []
    for m in d['media']:
        dst = os.path.join(out, 'images', m['id'] + '.webp')
        magick(os.path.join(SRC, 'assets', 'images', m['src']), '-resize', '%dx>' % m['width'], '-strip', '-quality', '78', dst)
        medias.append({'id': m['id'], 'file': 'demo/images/' + m['id'] + '.webp', 'title': m['title'], 'alt': m['alt']})
    # The chooser's preview: 600x400 from the set's main image.
    prev = next(m for m in d['media'] if m['id'] == d['preview'])
    os.makedirs(os.path.join(OUT, 'assets', 'demos'), exist_ok=True)
    magick(os.path.join(SRC, 'assets', 'images', prev['src']), '-resize', '600x400^', '-gravity', 'center', '-extent', '600x400', '-strip', '-quality', '72',
           os.path.join(OUT, 'assets', 'demos', d['slug'] + '.webp'))
    demo = {'version': 1, 'options': d['options'], 'categories': d['categories'], 'tags': d['tags'], 'media': medias,
            'pages': d['pages'], 'posts': d['posts'], 'menus': d['menus'], 'customizer': d['customizer']}
    with open(os.path.join(out, 'demo.json'), 'w') as f:
        json.dump(demo, f, indent=1, ensure_ascii=False)
        f.write('\n')
    print(d['slug'], len(d['pages']), 'pages', len(d['posts']), 'posts', len(medias), 'images')


for d in SETS:
    build(d)

# The theme's list of sets (Go): name, description and preview of each, in this order.
print('\n'.join('{Slug: %s, Name: %s, Description: %s, Image: "demos/%s.webp"},' % (json.dumps(d['slug']), json.dumps(d['name']), json.dumps(d['description']), d['slug']) for d in SETS))
