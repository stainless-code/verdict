Report a migration that destroys data irreversibly in the same step that ships
the code: `DROP TABLE`, `DROP COLUMN`, `TRUNCATE`, or a `DELETE`/`UPDATE`
without a `WHERE`. A rolled-back deploy cannot restore the rows.
