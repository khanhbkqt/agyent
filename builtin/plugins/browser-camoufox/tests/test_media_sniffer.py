"""
Unit tests for MediaSniffer and MediaHandler.
Tests parsing of decrypted YouTube streams, HLS/DASH manifest detection,
cross-platform FFmpeg header formatting, and stream classification.
"""

import os
import sys
import unittest
from unittest.mock import MagicMock, patch

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from core.media_sniffer import MediaSniffer
from handlers.media_handler import _build_ffmpeg_headers, _resolve_downloads_dir, handle_download_media


class TestMediaSniffer(unittest.TestCase):

    def setUp(self):
        self.sniffer = MediaSniffer(max_records=10)

    def test_parse_youtube_url(self):
        test_url = (
            "https://rr3---sn-4g5edn6r.googlevideo.com/videoplayback?"
            "expire=1725541200&ei=AbCdEf&ip=1.2.3.4&id=o-AbCdEf&itag=137&"
            "source=youtube&requiressl=yes&mime=video%2Fmp4&range=0-1048575&"
            "dur=245.500&sparams=expire%2Cei%2Cip%2Cid%2Citag%2Csource%2Crequiressl%2Cmime&"
            "sig=MEUCIQDOk123456789&lsig=AG3C_xyz"
        )
        res = MediaSniffer._parse_youtube_url(test_url)
        self.assertEqual(res["itag"], 137)
        self.assertEqual(res["mime"], "video/mp4")
        self.assertTrue(res["is_video"])
        self.assertFalse(res["is_audio"])
        self.assertEqual(res["dur"], 245.5)
        self.assertEqual(res["range"], "0-1048575")
        # Clean URL must NOT have range parameter
        self.assertNotIn("range=0-1048575", res["clean_url"])
        self.assertIn("ratebypass=yes", res["clean_url"])
        self.assertIn("itag=137", res["clean_url"])
        self.assertIn("sig=MEUCIQDOk123456789", res["clean_url"])

    def test_capture_youtube_streams(self):
        page_mock = MagicMock()
        self.sniffer.attach_to_page(page_mock)

        # Simulate video request
        req_video = MagicMock()
        req_video.url = "https://rr1---sn-abc.googlevideo.com/videoplayback?itag=137&mime=video%2Fmp4&sig=sig123&range=0-500"
        req_video.method = "GET"
        req_video.headers = {"user-agent": "Mozilla/5.0"}
        req_video.resource_type = "media"
        self.sniffer._handle_request(req_video)

        # Simulate audio request
        req_audio = MagicMock()
        req_audio.url = "https://rr1---sn-abc.googlevideo.com/videoplayback?itag=140&mime=audio%2Fmp4&sig=sig456&range=0-200"
        req_audio.method = "GET"
        req_audio.headers = {"user-agent": "Mozilla/5.0"}
        req_audio.resource_type = "media"
        self.sniffer._handle_request(req_audio)

        yt = self.sniffer.get_youtube_streams()
        self.assertEqual(len(yt["videos"]), 1)
        self.assertEqual(len(yt["audios"]), 1)
        self.assertEqual(yt["videos"][0]["itag"], 137)
        self.assertEqual(yt["audios"][0]["itag"], 140)

    def test_capture_manifests(self):
        req_hls = MagicMock()
        req_hls.url = "https://video.twimg.com/ext_tw_video/12345/pu/pl/master.m3u8"
        req_hls.method = "GET"
        req_hls.headers = {}
        req_hls.resource_type = "xhr"
        self.sniffer._handle_request(req_hls)

        req_dash = MagicMock()
        req_dash.url = "https://manifest.googlevideo.com/api/manifest/dash/id/xyz/manifest.mpd"
        req_dash.method = "GET"
        req_dash.headers = {}
        req_dash.resource_type = "xhr"
        self.sniffer._handle_request(req_dash)

        manifests = self.sniffer.get_manifests()
        self.assertEqual(len(manifests), 2)
        self.assertEqual(manifests[0]["type"], "hls")
        self.assertEqual(manifests[1]["type"], "dash")

    def test_capture_direct_media(self):
        req_tiktok = MagicMock()
        req_tiktok.url = "https://v16-webapp-prime.tiktokcdn.com/video/tos/useast2a/tos-useast2a-ve-0068c001/abc.mp4?expire=123"
        req_tiktok.method = "GET"
        req_tiktok.headers = {}
        req_tiktok.resource_type = "media"
        self.sniffer._handle_request(req_tiktok)

        direct = self.sniffer.get_direct_streams()
        self.assertEqual(len(direct), 1)
        self.assertIn("tiktokcdn.com", direct[0]["url"])


class TestMediaHandler(unittest.TestCase):

    def test_build_ffmpeg_headers(self):
        ua = "CustomMozilla/5.0"
        cookies = [
            {"name": "SID", "value": "xyz123"},
            {"name": "HSID", "value": "abc456"},
        ]
        referer = "https://www.youtube.com/watch?v=test"
        custom = {"Origin": "https://www.youtube.com"}

        headers_str = _build_ffmpeg_headers(
            user_agent=ua,
            cookies=cookies,
            referer=referer,
            custom_headers=custom,
        )

        # Must end with \r\n and contain properly delimited lines
        self.assertTrue(headers_str.endswith("\r\n"))
        self.assertIn("User-Agent: CustomMozilla/5.0\r\n", headers_str)
        self.assertIn("Referer: https://www.youtube.com/watch?v=test\r\n", headers_str)
        self.assertIn("Cookie: SID=xyz123; HSID=abc456\r\n", headers_str)
        self.assertIn("Origin: https://www.youtube.com\r\n", headers_str)

    def test_resolve_downloads_dir(self):
        # Specific output_dir
        tmp_dir = os.path.join(os.path.expanduser("~"), ".agyent", "test_downloads")
        res = _resolve_downloads_dir(agent_name="agent_a", output_dir=tmp_dir)
        self.assertEqual(res, os.path.abspath(tmp_dir))
        self.assertTrue(os.path.isdir(res))

    @patch("handlers.media_handler._get_ffmpeg_bin", return_value=None)
    def test_download_media_no_ffmpeg(self, mock_get_bin):
        # Should gracefully return actionable error when FFmpeg is not installed
        res = handle_download_media(video_url="https://example.com/video.mp4")
        self.assertFalse(res["success"])
        self.assertIn("FFmpeg executable not found in system PATH", res["error"])
        self.assertIn("winget install Gyan.FFmpeg", res["error"])

    @patch("shutil.which", return_value="/usr/bin/ffmpeg")
    @patch("subprocess.Popen")
    def test_download_media_dual_adaptive_command(self, mock_popen, mock_which):
        # Test command building for dual adaptive streams
        proc_mock = MagicMock()
        proc_mock.communicate.return_value = (b"", b"")
        proc_mock.returncode = 0
        mock_popen.return_value = proc_mock

        with patch("os.path.exists", return_value=True), patch("os.path.getsize", return_value=1048576):
            res = handle_download_media(
                video_url="https://example.com/video_1080p.mp4",
                audio_url="https://example.com/audio_140.mp4",
                output_filename="test_output.mp4",
                start_time="00:00:10",
                duration="15",
                accurate_trim=True,
            )

            self.assertTrue(res["success"])
            self.assertEqual(res["topology"], "dual_adaptive")

            # Check that Popen was called with list args and shell=False
            called_args, called_kwargs = mock_popen.call_args
            cmd = called_args[0]
            self.assertIsInstance(cmd, list)
            self.assertFalse(called_kwargs.get("shell", False))

            # Check flags: two inputs, start time, duration, accurate re-encode
            self.assertIn("-ss", cmd)
            self.assertIn("00:00:10", cmd)
            self.assertIn("-t", cmd)
            self.assertIn("15", cmd)
            self.assertIn("-map", cmd)
            self.assertIn("libx264", cmd)
            self.assertIn("ultrafast", cmd)


if __name__ == "__main__":
    unittest.main()
