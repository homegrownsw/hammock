-- Migration number: 0002 	 2026-11-18
-- Which push service a device's token is for: 'apns' (an iPhone) or 'fcm'
-- (an Android phone, through Firebase Cloud Messaging). Existing rows are
-- all iPhones. sandbox only means anything for 'apns'.
ALTER TABLE devices ADD COLUMN provider TEXT NOT NULL DEFAULT 'apns';
