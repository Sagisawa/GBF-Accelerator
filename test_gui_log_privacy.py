import unittest
from gui_main import mask_log_privacy


class TestLogPrivacyDesensitization(unittest.TestCase):
    """Unit tests for mask_log_privacy utility in gui_main.py."""

    def test_uid_masking(self):
        # uid parameter in query string
        line1 = "[14:36:26] [BYPASS-API ] 200 POST game.granbluefantasy.jp/ob/r?t=1789799786136&uid=9812230 (98ms, HTTP/1.1, reused)"
        res1 = mask_log_privacy(line1)
        self.assertNotIn("9812230", res1)
        self.assertIn("uid=******", res1)

        # uid as first query param
        line2 = "[14:36:26] [BYPASS-API ] 200 GET game.granbluefantasy.jp/rest/user/status?uid=123456&_p=1"
        res2 = mask_log_privacy(line2)
        self.assertNotIn("123456", res2)
        self.assertIn("?uid=******", res2)

    def test_timestamp_masking(self):
        line = "[14:36:27] [BYPASS-API ] 200 GET game.granbluefantasy.jp/rest/sound/quest_map_bgm?_=1789799786136&t=1789799786136"
        res = mask_log_privacy(line)
        self.assertNotIn("1789799786136", res)
        self.assertIn("_=***", res)
        self.assertIn("t=***", res)

    def test_token_and_session_masking(self):
        line = "[14:36:27] [BYPASS-API ] 200 POST game.granbluefantasy.jp/rest/auth?token=secret_abc_123&session=sess_999"
        res = mask_log_privacy(line)
        self.assertNotIn("secret_abc_123", res)
        self.assertNotIn("sess_999", res)
        self.assertIn("token=******", res)
        self.assertIn("session=******", res)

    def test_raid_battle_id_masking(self):
        # 11-digit battle instance ID in supporter_raid path
        line1 = "[14:36:28] [BYPASS-API ] 200 GET game.granbluefantasy.jp/quest/content/supporter_raid/46602483502/2/0?_=1789799786136"
        res1 = mask_log_privacy(line1)
        self.assertNotIn("46602483502", res1)
        self.assertIn("/supporter_raid/10000000000/2/0", res1)

        # 11-digit battle instance ID in multiraid/content/index path
        line2 = "[14:36:31] [BYPASS-API ] 200 GET game.granbluefantasy.jp/multiraid/content/index/46602483502?_=1789799786136"
        res2 = mask_log_privacy(line2)
        self.assertNotIn("46602483502", res2)
        self.assertIn("/multiraid/content/index/10000000000", res2)

    def test_user_profile_id_masking(self):
        line = "[14:36:30] [BYPASS-API ] 200 GET game.granbluefantasy.jp/profile/content/index/9812230"
        res = mask_log_privacy(line)
        self.assertNotIn("9812230", res)
        self.assertIn("/profile/content/index/******", res)

    def test_lan_client_ip_masking(self):
        # 192.168.x.x LAN IP masked
        line1 = "[14:36:25] [CONNECT    ] [192.168.1.108] prd-game-a-granbluefantasy.akamaized.net:443 -> MITM"
        res1 = mask_log_privacy(line1)
        self.assertNotIn("192.168.1.108", res1)
        self.assertIn("[192.168.1.*]", res1)

        # 10.x.x.x LAN IP masked
        line2 = "[14:36:25] [CONNECT    ] [10.0.12.55] prd-game-a-granbluefantasy.akamaized.net:443 -> MITM"
        res2 = mask_log_privacy(line2)
        self.assertNotIn("10.0.12.55", res2)
        self.assertIn("[10.0.12.*]", res2)

        # 127.0.0.1 loopback remains untouched
        line3 = "[14:36:25] [CONNECT    ] [127.0.0.1] prd-game-a-granbluefantasy.akamaized.net:443 -> MITM"
        res3 = mask_log_privacy(line3)
        self.assertIn("[127.0.0.1]", res3)

    def test_static_assets_and_quest_ids_not_corrupted(self):
        # Monster/summon asset filenames must not be accidentally modified
        line_asset = "[14:36:25] [CACHE-DISK ] GET  200    0ms [hit304] /assets/img/sp/quest/assets/summon/qm/2030000000_hard.png"
        res_asset = mask_log_privacy(line_asset)
        self.assertEqual(line_asset, res_asset)

        # Public quest ID (301061 is Lucilius HL) must not be treated as user privacy
        line_quest = "[14:36:28] [BYPASS-API ] 200 GET  game.granbluefantasy.jp/rest/quest/assist/search/assist_list/1/301061"
        res_quest = mask_log_privacy(line_quest)
        self.assertIn("301061", res_quest)

    def test_empty_or_none_line(self):
        self.assertEqual(mask_log_privacy(""), "")
        self.assertIsNone(mask_log_privacy(None))


if __name__ == "__main__":
    unittest.main()
