# prober

## Language

### Probing

**Target preparation**:
The step that turns a loaded manifest's targets into a Plan: structural validation, the default assertion, and compiled assertions for every enabled target. Run and lint share it.
_Avoid_: compilation, target compiling, validation (for the whole step)

**Plan**:
The prepared, ready-to-execute form of a manifest's targets, each enabled target paired with its compiled assertions. Execution accepts nothing else.
_Avoid_: compiled targets, prepared config
