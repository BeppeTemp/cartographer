---
topic: architecture
---

# D240 — A selection hidden by a filter stays selected, the graph draws none

**Decision.** When a Type or Status filter hides the selected concept, the
selection stays: the URL keeps `concept=` and the Inspector stays open. The
graph treats it as no selection: the filtered set is drawn at full colour, with
no labels and no signals, and the camera returns to the pose saved before the
focus. Clearing the filter, or re-including the concept, focuses it again. When
the selection is visible, its neighbourhood (the kept colours, the labels, the
signals) is always the visible neighbourhood, never a neighbour the filter hid.

**Why.** Filtering is a view operation. Before this, the graph kept the hidden
selection as its focus: every visible node receded into the canvas and the old
neighbourhood's names floated over nothing, so the graph looked empty. The
cost is that the Inspector can show a concept the graph does not draw.

**Alternatives rejected.**
- Clear the selection when a filter hides it: the reader loses their place,
  and the cleared `concept=` adds a history entry they did not ask for.
- Keep the hidden node drawn despite the filter: the filter would no longer
  mean what it says, and the visible count would disagree with the canvas.

**Consequences.** The fix lives in `GraphView.tsx`: an effective selection
(`selected` unless hidden) drives the colour and the selection effects, and the
selection effect depends on the visible set. It relies on being declared after
the visible-set effect, so it re-runs after `setData` in the same commit; a
comment beside it says so. `LivingScene` needs no change: an absent id already
hides the ring and stops the camera follow.
