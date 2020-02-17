#!/usr/bin/env python3

import requests

API = 'https://api.stripe.com'

def do_one(keys):
    url = '/v1/tokens'
    data = {
        'card': {
            'number': '4242424242424242',
            'exp_month': 2,
            'exp_year': 2021,
            'cvc': 314,
        },
    }
    headers = {
        'authorization': 'Bearer %s' % keys['public'],
    }
    r = requests.post(API + '/v1/tokens', data=data, headers=headers)
    r.raise_for_status()

    token = r.json()

    data = {
        'amount': 666,
        'currency': 'usd',
        'source': token['id'],
        'description': 'hi there.',
    }
    headers = {
        'authorization': 'Bearer %s' % keys['secret'],
    }

    r = requests.post(API + '/v1/charges', data=data, headers=headers)
    r.raise_for_status()

    print('charge status_code: %s (id %s)' % (r.status_code, r.json()['id']))


def main(ctx={}):
    keys = {
        'public': 'pk_test_NcsIuinz7RDJll45jhSBGgBr006F0r6UkQ',
        'secret': 'sk_test_blerg',
    }
    do_one(keys)


if __name__ == '__main__':
    exit(main())
