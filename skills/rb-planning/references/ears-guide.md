# EARS Notation Guide

EARS (Easy Approach to Requirements Syntax) produces specs that are testable by construction. A worker can read each one and know exactly what to verify.

## Patterns

- **Ubiquitous**: `The <system> shall <response>`
- **Event-driven**: `When <trigger>, the <system> shall <response>`
- **State-driven**: `While <precondition>, the <system> shall <response>`
- **Unwanted behavior**: `If <trigger>, then the <system> shall <response>`
- **Optional feature**: `Where <feature is included>, the <system> shall <response>`

## Good Specs (concrete, testable)

- `When cost is nil, the widget shall return empty string`
- `The function shall be registered as 'cost' in widget.Registry`
- `When the user presses 'e', the form overlay shall open with the selected card's data`
- `The skill shall write the design doc to .agent-history/YYYY-MM-DD-<topic>-design.md`

## Bad Specs (vague, untestable)

- `Handle errors properly`
- `Implement the feature correctly`
- `Make it robust`

## Minimum Specs Per Card

Every task card needs at minimum:

1. A spec naming the target file(s): `The implementation shall modify skills/rb-brainstorm/SKILL.md`
2. A spec for the happy path behavior
3. A spec for edge cases or empty/nil handling
4. A spec for tests (when applicable)

## Pinning Values

When a card introduces or changes constants, defaults, or magic numbers, add a spec pinning the values:

- `Default thresholds shall be: warn=60%, critical=80%`
- `The polling interval shall be 2 seconds`

Without this, a later card touching the same code may silently change the values.
