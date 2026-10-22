# Source kinds — what to extract, what to ignore

Use as the `source_kind` value of `source_register` (free string). The procedure never changes;
only what counts as a fact does.

**document** (report, spec, PDF, slides). Extract decisions, requirements, figures, definitions,
owners and dates; point to section or page. Ignore cover pages, tables of contents, boilerplate
and legal footers.

**transcript** (meeting recording or notes). Extract decisions, action items with owner and due
date, stated facts, and questions left open; point to a timestamp or speaker turn. Ignore small
talk, greetings, repetitions and filler. Attribute claims to the speaker, and do not promote a
guess to a fact.

**thread** (chat or forum conversation). Extract the conclusion the thread reached and the facts
it relied on; point to the message. Ignore reactions, off-topic branches and superseded
positions, but note when the thread never converged (that is an `open_question`).

**ticket** (issue, request, change record). Extract the symptom, the cause when stated, the
resolution, the affected components and the dates; point to the comment. Ignore status churn,
bot notifications and assignment changes.

**incident** (timeline, post-incident notes). Extract the sequence with times, the impact, the
root cause, the fix and the follow-ups; point to the timeline entry. Ignore blame and speculation
that was later disproved, unless the disproof itself is the lesson.

**email**. Extract decisions, commitments, dates and attachments' key facts; point to the message
and its date. Ignore signatures, disclaimers, quoted earlier replies and routing headers.

**web page**. Extract the claims the page owns, with the URL and retrieval date as locator and
timestamp; the page may change, so record what it said. Ignore navigation, ads, cookie banners
and related-links blocks.

**dataset** (CSV, export, table). Extract the schema, the scale (rows, period), notable values
and what the data cannot say; point to column or row range. Do not copy the data into pages:
summarize it, and keep the original outside the KB or as an asset of the Source.
