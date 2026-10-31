UPDATE bot_message_media
SET media_position = 'before',
    updated_at = now()
WHERE media_type = 'video_note'
  AND media_position <> 'before';
