#!/usr/bin/env python3

import requests


def do_one():
    url = 'https://api.stripe.com/v1/tokens'
    payload = {
        'card': {
            'number': '4242424242424242',
            'exp_month': 2,
            'exp_year': 2021,
            'cvc': 314,
        },
    }
    headers = {
        'authorization': 'Bearer pk_test_NcsIuinz7RDJll45jhSBGgBr006F0r6UkQ',
    }
    r = requests.post(url, data=payload, headers=headers)
    r.raise_for_status()

    # r = requests.get('https://github.com/timeline.json')
    print('r.status_code: %s (%s)' % (r.status_code, r.json()['id']))
    # print('r.status_code: %s (%s)' % (r.status_code, json.decode(r.text)['message']))
    # we expect an HTTP 410
    # r.raise_for_status()


def main(ctx={}):
    do_one()


if __name__ == '__main__':
    exit(main())
