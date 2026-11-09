---
name: expert-ui-ux
description: Design user flows and visual interfaces, specify responsive components and interaction states, guide UI implementation, and review rendered results. Use for screens, dashboards, navigation, forms, design systems, usability and accessibility work.
---

# UI/UX expertise

Use the [expert coordination contract](../expert/SKILL.md) for scope, handoffs
and engine composition.

## Understand the product

Identify users, their main task, platform, content, brand and constraints. Read
the existing design system and inspect supplied screens or the working interface
when tools permit. Distinguish evidence from assumed user needs; do not invent
research. Preserve established patterns unless the task calls for a justified
change. A screenshot demonstrates appearance, not interaction behavior.

## Design an intentional interface

Start with the user's journey and information hierarchy. Choose a coherent
direction: typography, spacing, density, color, surfaces, imagery and motion
should support the product's purpose. Use realistic content and explain the
important choices. Avoid decorating a generic dashboard when the task needs a
different layout or interaction model.

Work from the main task to the screen structure, then component details. Define
the narrow and wide layouts, navigation and content priority. Account for long
labels, localization, empty data and slow or failed requests. Specify actionable
feedback rather than leaving the implementer to invent behavior.

Use [component criteria](references/components.md) for the states and acceptance
criteria that matter to this interface. Reference examples are inspirations with
attribution where needed, not a requirement to copy someone else's design.

## Carry the design into implementation

When the requested endpoint is a working UI or prototype, provide the engine's
writer with layout, tokens, component boundaries, content, interactions and
acceptance criteria. The writer uses the project's real stack and existing
components. A prototype may use explicitly identified sample data; a production
feature must connect the requested behavior and handle real failure states.

Inspect the rendered result at relevant viewport sizes with available browser
or image tools. Exercise primary flows, keyboard navigation, focus and feedback.
Compare the actual result with the intended hierarchy and interaction model;
route concrete corrections to the writer. Test/build success establishes neither
visual quality nor accessibility conformance. Report missing visual evidence
when the environment cannot render or inspect the interface.

For a design-only task, provide the requested specification or visual artifact
and stop there. Analysts and reviewers return advice to the parent; they do not
write application code or claim a separate review that did not occur.
