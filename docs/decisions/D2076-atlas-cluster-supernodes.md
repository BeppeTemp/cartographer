---
topic: architecture
---

# D2076 — The cluster view is super-nodes in the graph, with no interface of its own

**Decision.** Above the cluster threshold the overview is mixed: the server
folds the largest communities (at least 10 members, at most 150) into
super-nodes, largest first, until at most 200 concepts and clusters are drawn,
and every other community stays open as plain concepts with their real edges,
so the picture says how large the KB is. No cluster is smaller than 10. Only
when the small communities alone exceed the budget are they grouped by their
dominant Map (`c:~<map>`), and what remains shares `c:~tail`. The Atlas draws
the super-nodes inside the graph, not around it. A super-node is a flat, unlit
hexagon, always facing the camera, in its cluster colour (its dominant Map's
hue, so none is grey and the legend, the rail and the graph agree), sized by the
square root of its member count between 1.6x and 2x an average concept, named "name ·
count" on hover (no always-on labels). Links between clusters are weighted
super-edges (three to eight strokes), and a link from an open concept to a
folded cluster ends on the super-node. The layout treats super-nodes as bodies:
springs along super-edges that are shorter and stiffer with their weight,
repulsion that grows with the glyph and reaches across the picture, a collision
of the glyph's drawn size plus a margin, and a centring, firmest on a cluster
with no super-edge, a spring tying each concept left open to the glyph it links or, with no link to a glyph, to a glyph of its own Map (else spread over all), so they lie among the glyphs and not on a ring or in a ball of their own (the layout is `lib/graph3d/clusterlayout.ts`, run by the
scene and by a test without WebGL).

At most one cluster is open, an accordion: opening another folds the first, and
`?unfold=` holds one id (the server opens the `focus` cluster, else the last id
that exists, and reports the others as dropped). Opening, folding and switching
are the simulation's own motion, the way the flat view takes a node that is
added: the members of the cluster opening are spread over its glyph and grow in,
the glyph that comes back is put where its members were, every other node starts
where it is, and the simulation is reheated (alpha 0.3 opening, 0.25 folding)
and cools at its own pace. An open cluster's members are concepts like any other:
their links to folded clusters are the springs of a concept to a glyph, so they
mix into the graph by its general forces; nothing outlines them, holds them in a
room or pushes the rest out of it (an outline, a room and a cohesion force were
each tried in the live session and read as a lobe standing apart). No glyph,
concept or member is dimmed; members and loose concepts are coloured by Map. A
folded cluster stands out by its shape: a hexagon with an inner contour and a
soft hexagonal aura of its colour. The camera keeps the whole graph in frame,
centred, in every state: for a graph of at most 600 nodes it aims from the
first frame at where the transition will come to rest (the simulation is run
ahead within 60 ms and put back), for a larger one it leads each node by its
velocity, and a transition's follow yields to a drag or a wheel. A breadcrumb
at the top left, "KB › cluster · count", closes it: the KB name folds it, and
Escape does; a click on the background only deselects. There is no banner,
level switch, ring, chip, or cluster list. The legend has a fixed width and
seven rows in both views: grouped, three clusters (the open one first, "name ·
count, open"), "N more clusters", two Maps of the open concepts and "N more
Maps"; whole, six Maps and "N more Maps". Colour is the Map's everywhere: there
is no Community mode. A KB served clustered has one control in the legend that
cycles Compact (an open cluster folds, and the concept selected in it closes, or
the server would unfold it again), Expand (`graph=nodes`: every concept, up to
7,500) and Group. A large KB always opens grouped: the choice is not remembered
(it was, and a KB left whole came back whole, which the maintainer did not
want). Expand and Group are a morph between two layouts of the same KB, one per
mode. Keyboard and screen readers reach each folded cluster as a button
"cluster <name>, <n> concepts". The `graph/clusters` response names each open
cluster's drawn members (`members`, positions in `nodes`, and the cluster's
`slot` and `collection`).

The threshold is 1,200 visible concepts. The whole view of a large KB shows
every concept up to `kb.MaxGraphNodeLimit`, 7,500 (30,000 edges), and stays
smooth there: above 1,500 nodes the entrance opens as a flower, from the centre
outwards and up to 2.6 times longer, the repulsion is coarser (theta 1.2) and
applied every other tick at double strength, and a costly simulation runs at
most one tick per frame, so the layout slows and the frames do not. A leaf
concept's colour recedes towards the background by its weight, a hub keeps its
Map's colour whole.

Links are budgeted where they would pile up: a cluster view aggregates them into
super-edges (the server returns at most 1,500 edges, heaviest first), and the
Nodes view and an open cluster draw and lay out at most 6,000 links, each node
keeping its strongest ones (towards its best-connected neighbours).

This supersedes the view paragraph of D1986 (one sphere on a log scale, one to
three strokes, the chips, the switch, the notice and the legend's cluster list);
the route, its parameters and its URL state in D1986 stand, but for the single
open cluster.

**Why.** The reader should meet a big KB as the same graph, with a new kind of
node that opens, not as a mode with its own notices and panels. The first design
narrated the projection around the canvas and drew every cluster as an
undifferentiated dot; the maintainer rejected it as heavy. Measuring decided the
budget: leaving about 1,300 concepts open made every layout tick cost about 10 ms
(repulsion grows with the node count, 1,336 nodes against 23), and a screen of
loose grey nodes; folding everything hid how large the KB is, which the user
did not want ("something open, something large, to stay under an acceptable
number of rendered nodes"). At 200 elements a tick is under 2 ms and the
response, at about 350 bytes an element, leaves room for one open cluster inside
150 KiB: on the 5,000-concept test KB the overview is 20 glyphs, 112 open
concepts, 183 edges and 44 KB, and one open cluster makes it 19 glyphs, 363
nodes, 758 edges and 134 KB. A single open cluster keeps the picture and the
payload bounded however much is clicked. Opening used to knock the whole
network; it is now local, which is what made it lag.

**Alternatives rejected.** The previous design (banner, switch, cluster legend,
chips of counts): it narrated the projection instead of drawing it. Folding
every concept (small communities grouped by Map): the overview then says nothing
of the KB's size. A cluster threshold of 500 or 2,000: at 500 a mid-sized KB lost its whole
picture, at 2,000 a flat tick costs about 9 ms (measured: 500 nodes 1.5 ms,
1,200 4.7 ms, 2,000 8.8 ms, 3,000 13.9 ms). Leaving communities open under a render budget of 1,500:
unreadable and four times the payload; a budget of 200 plus the 10-member floor
instead. Several clusters open at once: the payload and the layout grow with
each, and a pile of hulls is the clutter the accordion avoids. A cluster list in
the legend for the keyboard: the screen-reader buttons do it without a visible
list. Members by id in `unfolded`: positions are a tenth of the size. Weighting
an edge by a per-vertex alpha: it needs a custom line material; overlaid strokes
give the same on the stock one.

Showing the whole view cut to its top 2,000 by PageRank: the user wants to see
every concept of a 5,000-concept KB; measured on it, the thinned repulsion took
the whole view from about 30 to 80-100 frames a second. Moving the simulation
to a Web Worker would remove the last stalls, and is left to its own plan.

**Consequences.** The overview's glyph count depends on the budget, not on the
KB: a KB of many equal communities leaves one open. The Nodes view is reached
only by the legend's Expand, and the growth replay is off in the cluster view. A client reading `unfolded[].members` indexes into the same
response's `nodes`. A KB whose communities are all tiny is a handful of Map
groups, large ones: unfolding shows the top 400 of its members by PageRank.
Dropping links in the Nodes view means the selection's neighbourhood lacks the
dropped ones, which only happens past 6,000 links.
