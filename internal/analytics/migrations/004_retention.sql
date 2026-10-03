ALTER TABLE cache_shadow MODIFY COLUMN query String TTL toDateTime(ts) + INTERVAL 72 HOUR
;;
ALTER TABLE cache_shadow MODIFY COLUMN candidate_query String TTL toDateTime(ts) + INTERVAL 72 HOUR
;;
ALTER TABLE cache_shadow MODIFY COLUMN candidate_answer String TTL toDateTime(ts) + INTERVAL 72 HOUR
;;
ALTER TABLE cache_shadow MODIFY COLUMN actual_answer String TTL toDateTime(ts) + INTERVAL 72 HOUR
;;
ALTER TABLE routing_shadow MODIFY COLUMN prompt String TTL toDateTime(ts) + INTERVAL 72 HOUR
;;
ALTER TABLE routing_shadow MODIFY COLUMN cheap_answer String TTL toDateTime(ts) + INTERVAL 72 HOUR
;;
ALTER TABLE routing_shadow MODIFY COLUMN strong_answer String TTL toDateTime(ts) + INTERVAL 72 HOUR
