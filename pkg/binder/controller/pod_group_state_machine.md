# Pod Group State Machine

```mermaid
---
title: PodGrop DFA
---
	%%{ init: { 'fontFamily': 'monospace', 'flowchart': { 'curve': 'cardinal' } } }%%
flowchart TD
  Pending --created >= min--> PreScheduling
  PreScheduling -.created < min.-> Pending
	Pending -.scheduled >= min.-> Scheduled
	PreScheduling --scheduled >= min--> Scheduled
	Pending --timeout--> Timeout
	PreScheduling --timeout--> Timeout
	
	subgraph finalState[Final State]
		Scheduled
		Timeout
	end
	
	style Timeout fill:#faa,color:black,font-weight:bold,stroke-width:2px,stroke:grey
	style Scheduled fill:#dff,color:black,font-weight:bold,stroke-width:2px,stroke:grey
	
	style finalState fill:#f8ffff,stroke:#333,stroke-width:1px,color:#,stroke-dasharray: 5 5
	style Pending fill:#d2dfff
	style PreScheduling fill:#d2dfff
```

**Explanation**: Based on the definition of [DFA](https://en.wikipedia.org/wiki/Deterministic_finite_automaton), let us use `State` to represent $Q$ and use `Msg` to represent $\Sigma$, then we have the following state-transition table.

| State \ Msg   | created < min | created >= min && scheduled < min | created >= min && scheduled >= min | timeout |
| ------------- | ------------- | --------------------------------- | ---------------------------------- | ------- |
| Pending       | -             | PreScheduling                     | Scheduled (Unexpected)             | Timeout |
| PreScheduling | Pending       | -                                 | Scheduled                          | Timeout |
| Scheduled     | -             | -                                 | -                                  | -       |
| Timeout       | -             | -                                 | -                                  | -       |

Please note that if both `created >= min && scheduled >= min` and `timeout` can be satisfied at the same time, it should be considered as **Scheduled**. We will use the `reentrant lock mechanism`<sup>[1]</sup> to ensure that the final state does not change.

> <sup>[1]</sup> Refer to `godel.bytedance.com/podgroup-final-op-lock` annotation