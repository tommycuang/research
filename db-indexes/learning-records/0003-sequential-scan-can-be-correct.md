# A planner can ignore an existing index

The learner recognized that a broad status query scans nearly the whole table and that the same low-cardinality index is more useful for a smaller failed subset. Corrected wording: the broad query returns about 85%, not all rows; an index path would require heap access across many pages, while a sequential scan reads those pages in physical order.
