-- QQ 官方机器人的 msg_id 长度不固定（实测单聊 117 字符、群聊超过 128），平台未承诺上限，改为 TEXT。
ALTER TABLE messages ALTER COLUMN platform_msg_id TYPE TEXT;
ALTER TABLE conversations ALTER COLUMN last_inbound_msg_id TYPE TEXT;
