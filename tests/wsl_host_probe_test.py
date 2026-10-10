import base64
import unittest

from wsl_host_probe import decode_wsl_output, expected_launch_error


class WSLOutputTests(unittest.TestCase):
    def decode(self, raw):
        return decode_wsl_output(base64.b64encode(raw).decode())[0]

    def test_utf8_linux_json_and_relay_diagnostic(self):
        for value in ['{"argv":["中文","","\\\\"]}\n',
                      '<3>WSL relay: getpwnam failed\n', '']:
            with self.subTest(value=value):
                self.assertEqual(self.decode(value.encode()), value)

    def test_native_utf16_errors_with_and_without_bom(self):
        for code in ['Wsl/Service/WSL_E_DISTRO_NOT_FOUND',
                     'Wsl/WSL_E_USER_NOT_FOUND']:
            value = '找不到指定的对象。\r\n错误代码: ' + code + '\r\n'
            for encoding in ['utf-16le', 'utf-16', 'utf-16be']:
                with self.subTest(code=code, encoding=encoding):
                    raw = value.encode(encoding)
                    if encoding == 'utf-16be':
                        raw = b'\xfe\xff' + raw
                    self.assertEqual(self.decode(raw), value)

    def test_corrupt_bytes_are_not_replaced_or_silently_accepted(self):
        for raw in [b'\xff', b'a\x00b\x00', '\ufffd'.encode(), b'\xff\xfea']:
            with self.subTest(raw=raw):
                with self.assertRaises((UnicodeError, ValueError)):
                    self.decode(raw)

    def test_missing_target_requires_specific_error_code(self):
        for name, code in [('missing_distro', 'WSL_E_DISTRO_NOT_FOUND'),
                           ('missing_user', 'WSL_E_USER_NOT_FOUND')]:
            self.assertTrue(expected_launch_error(name, {
                'exit_code': -1, 'stdout': '错误代码: Wsl/' + code, 'stderr': ''}))
            for status, text in [(0, code), (None, code), (-1, 'Access denied'),
                                 (-1, 'WSL_E_DIFFERENT_FAILURE')]:
                self.assertFalse(expected_launch_error(name, {
                    'exit_code': status, 'stdout': text, 'stderr': ''}))


if __name__ == '__main__':
    unittest.main()
