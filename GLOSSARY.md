# prober

## Language

### Probing

**Target preparation**:
The step that loads a manifest and turns its targets into a Plan, with structural checks, target defaults, the default assertion, and compiled assertions for enabled targets. Run and lint share it.
_Avoid_: compilation, target compiling, validation for the whole step

**Plan**:
The prepared, ready-to-execute form of a manifest's targets, each enabled target paired with its compiled assertions. Execution accepts nothing else.
_Avoid_: compiled targets, prepared config
