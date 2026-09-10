-- Reverse 0017: put the British spelling back in all three columns.
--
-- Same two targets, same guards, same narrow token replace on the JSON
-- body. A downgrade leaves no row carrying a value the older binary cannot
-- read — though the older binary read both spellings anyway, since v0.4.x
-- recognizes the US value too. The rollback exists because every migration
-- here has one.
--
-- envelopes.status, interactions.policy and interactions.terminal_error_code
-- are untouched on the way down for the same reasons they are untouched on the
-- way up.

UPDATE envelopes
   SET error_code = 'user-cancelled'
 WHERE error_code = 'user-canceled';

UPDATE envelopes
   SET response_payload = replace(response_payload, '"code":"user-canceled"', '"code":"user-cancelled"')
 WHERE response_kind = 'error'
   AND response_payload LIKE '%"code":"user-canceled"%';
