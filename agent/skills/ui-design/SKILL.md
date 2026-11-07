---
name: ui-design
description: Use when writing, reviewing, or modifying any user interface — QML (*.qml, *.qmldir, qmltypes), Qt Widgets, web (HTML/CSS/JSX/TSX/Vue/Svelte), Android (Compose, XML layouts), iOS (SwiftUI), TUI, or any other UI surface — ensures usable, accessible, responsive, performant interfaces and prevents the recurring UI failure modes (layout breaks under resize, hidden state, hardcoded sizes, dead controls, blocking UI thread, no feedback, inaccessible)
---

# UI Design

Universal rules first. QML specifics last. Apply both when the file is `.qml`.

## Core Principles

| # | Rule | Failure it prevents |
|---|------|---------------------|
| 1 | Clarity over cleverness | Cute UI nobody understands |
| 2 | Consistency: same thing → same look + same behavior everywhere | Cognitive load, surprise |
| 3 | Affordance: control looks like what it does (button = button, link = link) | Dead-zone clicks |
| 4 | Feedback: every user action produces visible response within 100ms | "Did it register?" double-clicks |
| 5 | Forgiveness: undo > confirm > nothing. Confirm only destructive irreversible actions | Lost work, alert fatigue |
| 6 | Hierarchy: visual weight matches importance | Primary action lost in noise |
| 7 | Progressive disclosure: hide complexity behind expand/advanced | Overwhelm on first use |
| 8 | One primary action per screen | Choice paralysis |
| 9 | Defaults that work for 80% of users | Forced configuration |
| 10 | Platform conventions over invention | Users re-learning basics |

## States — every view must handle all of them

| State | Required treatment |
|-------|-------------------|
| Empty | Explain *why* it's empty + next action ("No items. Add one.") |
| Loading | Skeleton or spinner ≥200ms; nothing if <200ms |
| Partial | Show what loaded; mark the rest pending |
| Error | What broke, why, how to recover. Never raw stack |
| Success | Confirm + clear path forward |
| Offline / degraded | Visible banner, disable non-functional controls |

Forgetting one state is the most common UI bug. Enumerate explicitly per view.

## Layout & Responsiveness

| Rule | Apply |
|------|-------|
| Layout containers, not absolute coordinates | RowLayout/ColumnLayout/GridLayout (QML); flex/grid (web); ConstraintLayout (Android) |
| Anchors are relative, never `x:`/`y:` literals for static layout | QML — `x`/`y` only for animation |
| Size in logical units, not raw pixels | `Screen.pixelDensity`, `dp`/`sp`, `rem`, `em`, `Theme.spacing` |
| Test at min/max window size + DPR 1× and 2× | Catch overflow, clipping, illegible text |
| Touch targets ≥ 44 px logical (mouse: ≥ 24 px) | Apple HIG / Material |
| Wrap, don't clip text | `wrapMode: Text.Wrap`, `overflow-wrap: anywhere` |
| Reserve space for variable content | Prevent layout jump when content arrives |

## Feedback & Latency

| Window | Treatment |
|--------|-----------|
| < 100 ms | Feels instant — no indicator |
| 100 ms – 1 s | Cursor / button-pressed state |
| 1 s – 10 s | Spinner with what's happening |
| > 10 s | Progress bar with %, ETA, cancel button |

Never block the UI thread. Long work → background. Show progress. Allow cancel.

## Accessibility (non-negotiable)

| Check | Threshold |
|-------|-----------|
| Color contrast | WCAG AA: 4.5:1 text, 3:1 large/UI |
| Keyboard reachable | Every interactive element via Tab; visible focus ring |
| Screen-reader names | `Accessible.name` (QML), `aria-label`, `contentDescription` |
| Don't rely on color alone | Add icon / text / pattern |
| Respect OS font scaling | No fixed `font.pixelSize: 12` — scale via theme |
| Respect reduced-motion | Skip non-essential animation when set |

## Performance

| Rule | Why |
|------|-----|
| Lazy-load offscreen / heavy content | `Loader { active: ... }` (QML), virtualization (lists) |
| Virtualize long lists | `ListView` reuses delegates; never `Repeater` over 100 items |
| Image: size to display + cache | Avoid full-res thumbnails |
| Animate transform/opacity, not layout | GPU-cheap, no relayout |
| 60 fps budget = 16 ms/frame | Profile on slowest target device |
| Bind cost: avoid expensive expressions in bindings | Recomputed on every dep change |

## Errors & Destructive Actions

- Error message format: **what** + **why** + **how to recover**. No "Error: -1".
- Destructive action: name the consequence in the button (`Delete forever`), not "OK".
- Confirmation dialog only when the action is destructive and irreversible. Otherwise: do it + offer Undo.
- Undo lifetime: long enough to read the toast + react (≥ 5 s; 10 s for destructive).

## QML Specifics

| Rule | Counter-example |
|------|-----------------|
| Declarative bindings, not imperative `onChanged` mutations | `onWidthChanged: height = width * 2` → `height: width * 2` |
| `id:` only on items referenced elsewhere | Don't id every `Rectangle` |
| `Layouts` for resizable; `anchors` for fixed-relative; never both on same item | `Layout.fillWidth: true` AND `anchors.left:` → undefined |
| `Component`/`Loader` for conditional heavy subtrees, not `visible: false` | `visible: false` still constructs + binds |
| `ListView` + `delegate`, never `Repeater` for unbounded lists | Repeater builds all at once |
| `States` + `Transitions` for mode changes | Manual `if` chains in handlers |
| `Connections { target: foo; function onSig() {} }` for cross-object signals | Don't reach across via global ids |
| `pragma Singleton` + `qmldir` for shared state / theme | Not a global JS object |
| Colors / sizes / fonts via Theme singleton, never literal | `color: "#3a3a3a"` → `color: Theme.bgSecondary` |
| `font.pointSize`/scaled units, not `font.pixelSize` literal | OS scaling support |
| Avoid binding loops: `width: parent.width; parent.width: childrenRect.width` | Watch console for `QML Binding loop` |
| Expensive logic → C++/backend, expose via properties/signals | Heavy JS on UI thread janks |
| `asynchronous: true` on `Loader`/`Image` for non-critical | Avoid first-frame stall |
| `clip: true` only when needed (extra render pass) | Default false |
| `RowLayout`/`ColumnLayout`/`GridLayout` need `Layout.*` attached on children | Plain `width`/`height` ignored |
| Use `Qt.callLater` to defer until binding settles | Prevents intermediate state read |

## File / Component Structure

- One component per file. File name = component name = `PascalCase.qml`.
- Component < 300 lines; split into sub-components when larger.
- Public properties at top, private (`_camelCase` + `QtObject` `d:`) below, signals next, then UI tree, then JS functions last.
- No magic numbers in the tree. Promote to named property or Theme.
- No business logic in the view. Wire to a ViewModel/controller exposed from C++.

## Anti-Patterns

| Smell | Fix |
|-------|-----|
| `Rectangle { width: 1920; height: 1080 }` | Bind to parent / `Screen` |
| `font.pixelSize: 14` | Use scaled / theme unit |
| `color: "#aabbcc"` literal in component | Theme |
| `visible: someExpensiveFlag` on heavy subtree | `Loader { active: someExpensiveFlag }` |
| `MouseArea { onClicked: { huge JS } }` | Move logic to backend; emit signal |
| Modal dialog for non-blocking info | Toast/snackbar |
| "Are you sure?" on every save | Just save; offer Undo |
| Spinner forever on error | Switch to error state |
| Same icon, two different actions | Differentiate |
| Disabled control with no tooltip explaining why | Add reason |

## Verification Checklist (run before claiming done)

1. Resize window: min, default, max — no clipping, overflow, illegible.
2. Tab through every interactive element — focus visible, order logical.
3. Trigger every state: empty, loading, error, success, offline.
4. Test on slowest target device — 60 fps maintained, no jank on interaction.
5. Color contrast checked (browser devtools, `qmlscene` + screenshot + contrast tool).
6. Screen reader announces every control with meaningful name.
7. No console warnings: binding loops, missing properties, deprecated APIs.
8. Destructive actions named explicitly + reversible (undo) where possible.
9. OS font-scale ×1.5 still readable, not clipped.
10. Reduced-motion OS setting respected.
