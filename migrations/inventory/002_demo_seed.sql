INSERT INTO inventory.stocks (sku_id, available, reserved, version)
VALUES ('SKU1', 100, 0, 1)
ON CONFLICT (sku_id) DO NOTHING;
