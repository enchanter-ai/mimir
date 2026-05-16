---
name: Spec clarification
about: Ambiguity or missing detail in the Provenance Envelope specification.
title: "spec: <section> <one-line question>"
labels: spec, clarification
---

<!--
For PROPOSED CHANGES to the spec wire format or validation algorithm, open a
Discussion in the `spec` board instead — those need community consensus
before a PR. This template is for genuine ambiguities you've encountered
while implementing a verifier or producer.
-->

## Spec section affected
<!-- e.g. § 9.3 canonical-form bytes, § 10.2 verification algorithm, § 15.4 threat model -->

## What's ambiguous

<!-- Quote the exact text from `spec/index.mdx` that you found unclear. -->

## What two (or more) reasonable interpretations exist

<!-- Spell them out — interpretation A vs interpretation B — so the maintainer
     can clarify in the right direction. If reference impls disagree, paste the
     divergent code paths. -->

**Interpretation A:**

**Interpretation B:**

## Which one did you implement (if applicable)

<!-- For an implementer's perspective: what did you actually do? Did it interop
     with the Go issuer / Rust verifier? -->

## Suggested clarification wording

<!-- Draft text the maintainer can drop into spec/index.mdx. Doesn't have to
     be polished — even "X means Y, not Z" is helpful. -->
